package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/security"
)

// inviteGracePeriod is how long a spent invite is kept past its expiry, so
// that someone redeeming a code that ran out an hour ago gets told it expired
// rather than being told it never existed.
const inviteGracePeriod = 7 * 24 * time.Hour

// Friends is how people find each other on Amici, and it is defined as much by
// what it does not offer as by what it does.
//
// There is no search. There is no directory, no "people you may know", no
// contact upload, no mutual-friends list, no suggested follows and no way to
// browse a friend's friends. Those features are how a network built for
// friends turns into a network built for reach, and they are the reason a
// stranger can find a fourteen year old on the platforms Amici is a reaction
// to.
//
// That leaves exactly two ways to reach somebody, both of which require them
// to have given you something first:
//
//  1. Their email address. You knew it before you came here, which means you
//     know them from somewhere real.
//
//  2. A request identifier they minted and handed to you, which stops working
//     twenty four hours later.
//
// Both routes end in a request the recipient has to accept. Neither route ever
// tells the sender whether the person they reached for is actually here.
type Friends struct {
	deps    Deps
	limiter *limiter
}

// EmailRequest asks to be friends with whoever owns an email address.
type EmailRequest struct {
	Email     string
	Note      string
	ClientKey string
}

// RequestByEmail sends a friend request to the owner of an email address.
//
// The critical property is that the caller learns nothing. Whether the address
// belongs to nobody, belongs to somebody who has switched off email requests,
// belongs to a young member, belongs to somebody who has blocked them, or
// belongs to a friend they already have, the answer is the same sentence and
// the same lack of detail. Everything interesting happens silently on the
// other side.
//
// This is why the function returns no indication of what it did.
func (f *Friends) RequestByEmail(ctx context.Context, actor *domain.Account, in EmailRequest) error {
	if err := requireCapability(actor, domain.CapSendFriendRequests); err != nil {
		return err
	}

	// The rate limits are the real defence for this route. Even a perfectly
	// silent response is an oracle if you can ask ten thousand times and
	// watch which addresses later show up as friends.
	if !f.limiter.allow("friendreq:hour:"+string(actor.ID), emailRequestsPerHour, time.Hour) {
		return fmt.Errorf("%w: you have sent a lot of requests in the last hour. Please try again later", domain.ErrRateLimited)
	}
	if in.ClientKey != "" && !f.limiter.allow("friendreq:client:"+in.ClientKey, emailRequestsPerClientPerHour, time.Hour) {
		return fmt.Errorf("%w: too many requests from here. Please try again later", domain.ErrRateLimited)
	}

	now := f.deps.Clock.Now()
	sentToday, err := f.deps.Store.CountRequestsSentSince(ctx, actor.ID, now.Add(-24*time.Hour))
	if err != nil {
		return fmt.Errorf("count sent requests: %w", err)
	}
	if sentToday >= emailRequestsPerDay {
		return fmt.Errorf(
			"%w: you have reached the limit of %d friend requests a day. It is a low limit on purpose: it is what stops this form being used to work out who is on Amici",
			domain.ErrRateLimited, emailRequestsPerDay,
		)
	}

	email, err := domain.ValidateEmail(in.Email)
	if err != nil {
		return err
	}
	note, err := domain.ValidateFriendRequestNote(in.Note)
	if err != nil {
		return err
	}

	target, err := f.deps.Store.AccountByEmail(ctx, domain.NormaliseEmail(email))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			// Nothing to do, and nothing to say. Note that we still consumed
			// a slot from the daily limit above: a miss has to cost the same
			// as a hit or counting attempts becomes the oracle.
			f.deps.Logger.Debug("friend request to an address with no account", "actor", actor.ID)
			return nil
		}
		return fmt.Errorf("look up account by email: %w", err)
	}

	if err := f.deliver(ctx, actor, target, domain.OriginEmail, note, now); err != nil {
		// Delivery problems that are the recipient's business, not the
		// sender's, are swallowed here. A real fault still surfaces.
		if errors.Is(err, domain.ErrForbidden) || isConflict(err) || errors.Is(err, domain.ErrNotFound) {
			f.deps.Logger.Debug("friend request not delivered", "actor", actor.ID, "reason", err)
			return nil
		}
		return err
	}
	return nil
}

// deliver applies the rules that decide whether a request actually reaches
// someone, and creates it if so. Both routes funnel through here so the rules
// cannot drift apart.
func (f *Friends) deliver(
	ctx context.Context,
	from *domain.Account,
	to *domain.Account,
	origin domain.RequestOrigin,
	note string,
	now time.Time,
) error {
	if from.ID == to.ID {
		return fmt.Errorf("%w: you are already your own friend", domain.ErrValidation)
	}
	if to.Status != domain.StatusActive {
		return fmt.Errorf("%w: that account is not active", domain.ErrForbidden)
	}
	if origin == domain.OriginEmail && !to.AcceptsEmailRequests(now) {
		// Either they have turned the email route off, or they are a young
		// member for whom it is never on. Knowing a child's email address does
		// not entitle you to reach them.
		return fmt.Errorf("%w: that person cannot be reached by email address", domain.ErrForbidden)
	}

	blocked, err := f.deps.Store.BlockExistsEitherWay(ctx, from.ID, to.ID)
	if err != nil {
		return fmt.Errorf("check block: %w", err)
	}
	if blocked {
		return fmt.Errorf("%w: that person cannot be reached", domain.ErrForbidden)
	}

	already, err := f.deps.Store.AreFriends(ctx, from.ID, to.ID)
	if err != nil {
		return fmt.Errorf("check friendship: %w", err)
	}
	if already {
		return fmt.Errorf("%w: you are already friends", domain.ErrConflict)
	}

	if existing, err := f.deps.Store.PendingRequestBetween(ctx, from.ID, to.ID); err == nil {
		if existing.FromID == to.ID {
			// They reached out first. Rather than stacking a second request,
			// treat this as an acceptance: two people trying to add each other
			// clearly want to be friends.
			return f.accept(ctx, existing, now)
		}
		return fmt.Errorf("%w: you already have a request waiting with that person", domain.ErrConflict)
	} else if !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("look for pending request: %w", err)
	}

	req := &domain.FriendRequest{
		ID:        domain.NewID(),
		FromID:    from.ID,
		ToID:      to.ID,
		State:     domain.RequestPending,
		Origin:    origin,
		Note:      note,
		CreatedAt: now,
	}
	if err := f.deps.Store.CreateFriendRequest(ctx, req); err != nil {
		return fmt.Errorf("create friend request: %w", err)
	}
	return nil
}

// MintedInvite is a freshly created request identifier. The plain code is
// returned exactly once, here, and never again from anywhere: only its keyed
// hash is stored.
type MintedInvite struct {
	Invite domain.Invite
	Code   string
	// Link is the code wrapped in a URL, for people who would rather paste a
	// link into a message than read letters out loud.
	Link string
	// ExpiresIn is how long the recipient has.
	ExpiresIn time.Duration
}

// MintInvite creates a short-lived request identifier.
func (f *Friends) MintInvite(ctx context.Context, actor *domain.Account, label string) (*MintedInvite, error) {
	if err := requireCapability(actor, domain.CapSendFriendRequests); err != nil {
		return nil, err
	}
	label, err := domain.ValidateInviteLabel(label)
	if err != nil {
		return nil, err
	}

	now := f.deps.Clock.Now()
	active, err := f.deps.Store.CountActiveInvites(ctx, actor.ID, now)
	if err != nil {
		return nil, fmt.Errorf("count active invites: %w", err)
	}
	if active >= maxActiveInvites {
		return nil, fmt.Errorf(
			"%w: you already have %d codes waiting to be used. Let one expire or cancel one before making another",
			domain.ErrConflict, active,
		)
	}

	code, err := security.NewInviteCode()
	if err != nil {
		return nil, fmt.Errorf("mint invite code: %w", err)
	}
	ttl := actor.InviteTTLFor(now)
	inv := &domain.Invite{
		ID:        domain.NewID(),
		OwnerID:   actor.ID,
		CodeHash:  security.HashInviteCode(f.deps.Secret, code),
		Label:     label,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := f.deps.Store.CreateInvite(ctx, inv); err != nil {
		return nil, fmt.Errorf("store invite: %w", err)
	}

	return &MintedInvite{
		Invite:    *inv,
		Code:      code,
		Link:      fmt.Sprintf("%s/friends/redeem?code=%s", f.deps.BaseURL, code),
		ExpiresIn: ttl,
	}, nil
}

// RevokeInvite cancels a code the member has shared.
func (f *Friends) RevokeInvite(ctx context.Context, actor *domain.Account, inviteID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !inviteID.Valid() {
		return notFound("invite")
	}
	if err := f.deps.Store.RevokeInvite(ctx, inviteID, actor.ID, f.deps.Clock.Now()); err != nil {
		return err
	}
	return nil
}

// Invites lists the member's own codes, with their state.
func (f *Friends) Invites(ctx context.Context, actor *domain.Account) ([]domain.Invite, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	return f.deps.Store.InvitesForOwner(ctx, actor.ID)
}

// RedeemInvite claims a request identifier, which sends a friend request to
// whoever minted it.
//
// Redeeming does not make you friends on its own. The code's owner still has
// to accept, because a code shared in a group chat or forwarded on can end up
// with someone the owner did not mean to reach, and the last word about who is
// in your life should always be yours.
func (f *Friends) RedeemInvite(ctx context.Context, actor *domain.Account, rawCode, note, clientKey string) (*domain.AccountCard, error) {
	if err := requireCapability(actor, domain.CapSendFriendRequests); err != nil {
		return nil, err
	}
	if !f.limiter.allow("invite:account:"+string(actor.ID), inviteAttemptsPerAccount, inviteAttemptWindow) {
		return nil, fmt.Errorf("%w: too many attempts at a request identifier. Please wait a while", domain.ErrRateLimited)
	}
	if clientKey != "" && !f.limiter.allow("invite:client:"+clientKey, inviteAttemptsPerClient, inviteAttemptWindow) {
		return nil, fmt.Errorf("%w: too many attempts from here. Please wait a while", domain.ErrRateLimited)
	}

	code, err := security.NormaliseInviteCode(rawCode)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}
	note, err = domain.ValidateFriendRequestNote(note)
	if err != nil {
		return nil, err
	}

	now := f.deps.Clock.Now()
	inv, err := f.deps.Store.InviteByCodeHash(ctx, security.HashInviteCode(f.deps.Secret, code))
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, fmt.Errorf("%w: we do not recognise that request identifier. Codes are only good for a day, so it may have expired", domain.ErrNotFound)
		}
		return nil, fmt.Errorf("look up invite: %w", err)
	}

	// Distinguish the reasons an invite is unusable, because each one has a
	// different thing for the member to do about it. This leaks nothing: they
	// already hold the code.
	switch {
	case inv.RevokedAt != nil:
		return nil, fmt.Errorf("%w: that request identifier was cancelled by the person who made it", domain.ErrExpired)
	case inv.RedeemedAt != nil:
		return nil, fmt.Errorf("%w: that request identifier has already been used. They are good for one person only", domain.ErrExpired)
	case !now.Before(inv.ExpiresAt):
		return nil, fmt.Errorf("%w: that request identifier expired at %s. Ask for a fresh one", domain.ErrExpired, inv.ExpiresAt.Format("15:04 on 2 January"))
	}

	if inv.OwnerID == actor.ID {
		return nil, fmt.Errorf("%w: that is your own request identifier", domain.ErrValidation)
	}

	owner, err := f.deps.Store.AccountByID(ctx, inv.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("%w: we could not find the person who made that code", domain.ErrNotFound)
	}

	// Claim the code before creating the request. If two people race on the
	// same code, exactly one of them gets past this line.
	if err := f.deps.Store.RedeemInvite(ctx, inv.ID, actor.ID, now); err != nil {
		return nil, err
	}

	if err := f.deliver(ctx, actor, owner, domain.OriginInvite, note, now); err != nil {
		return nil, err
	}

	card := owner.Card()
	return &card, nil
}

// Accept turns a pending request into a friendship.
func (f *Friends) Accept(ctx context.Context, actor *domain.Account, requestID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	req, err := f.loadIncoming(ctx, actor, requestID)
	if err != nil {
		return err
	}
	return f.accept(ctx, req, f.deps.Clock.Now())
}

// accept resolves a request and creates the friendship. It is shared with the
// "you both reached out at once" path in deliver, where the person accepting
// is not the caller.
func (f *Friends) accept(ctx context.Context, req *domain.FriendRequest, now time.Time) error {
	// Resolving first is what makes this safe under a double click: the
	// store's update requires the request to still be pending, so the second
	// attempt is rejected before any friendship is created.
	if err := f.deps.Store.ResolveFriendRequest(ctx, req.ID, domain.RequestAccepted, now); err != nil {
		return err
	}
	if err := f.deps.Store.CreateFriendship(ctx, req.FromID, req.ToID, now); err != nil {
		if isConflict(err) {
			// Already friends by some other path. Nothing to undo.
			return nil
		}
		return fmt.Errorf("create friendship: %w", err)
	}
	return nil
}

// Decline turns down a pending request. The sender is not told; they simply
// never hear back, which is a kinder and safer default than a notification
// that invites another attempt.
func (f *Friends) Decline(ctx context.Context, actor *domain.Account, requestID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	req, err := f.loadIncoming(ctx, actor, requestID)
	if err != nil {
		return err
	}
	return f.deps.Store.ResolveFriendRequest(ctx, req.ID, domain.RequestDeclined, f.deps.Clock.Now())
}

// Cancel withdraws a request the member sent.
func (f *Friends) Cancel(ctx context.Context, actor *domain.Account, requestID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !requestID.Valid() {
		return notFound("friend request")
	}
	req, err := f.deps.Store.FriendRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if req.FromID != actor.ID {
		return notFound("friend request")
	}
	return f.deps.Store.ResolveFriendRequest(ctx, req.ID, domain.RequestCancelled, f.deps.Clock.Now())
}

// loadIncoming fetches a request addressed to the actor, treating one
// addressed to anybody else as though it does not exist.
func (f *Friends) loadIncoming(ctx context.Context, actor *domain.Account, requestID domain.ID) (*domain.FriendRequest, error) {
	if !requestID.Valid() {
		return nil, notFound("friend request")
	}
	req, err := f.deps.Store.FriendRequestByID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if req.ToID != actor.ID {
		return nil, notFound("friend request")
	}
	if req.State != domain.RequestPending {
		return nil, fmt.Errorf("%w: that request has already been dealt with", domain.ErrConflict)
	}
	return req, nil
}

// Unfriend ends a friendship from either side.
func (f *Friends) Unfriend(ctx context.Context, actor *domain.Account, otherID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !otherID.Valid() {
		return notFound("friend")
	}
	friends, err := f.deps.Store.AreFriends(ctx, actor.ID, otherID)
	if err != nil {
		return fmt.Errorf("check friendship: %w", err)
	}
	if !friends {
		return notFound("friend")
	}
	return f.deps.Store.DeleteFriendship(ctx, actor.ID, otherID)
}

// Block stops someone reaching the member by any route, and ends the
// friendship if there was one.
func (f *Friends) Block(ctx context.Context, actor *domain.Account, otherID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !otherID.Valid() || otherID == actor.ID {
		return notFound("account")
	}
	if _, err := f.deps.Store.AccountByID(ctx, otherID); err != nil {
		return notFound("account")
	}
	return f.deps.Store.CreateBlock(ctx, &domain.Block{
		BlockerID: actor.ID,
		BlockedID: otherID,
		CreatedAt: f.deps.Clock.Now(),
	})
}

// Unblock lifts a block without restoring the friendship.
func (f *Friends) Unblock(ctx context.Context, actor *domain.Account, otherID domain.ID) error {
	if actor == nil {
		return domain.ErrUnauthenticated
	}
	if !otherID.Valid() {
		return notFound("account")
	}
	return f.deps.Store.DeleteBlock(ctx, actor.ID, otherID)
}

// RequestView is a pending request with the other person's card attached.
type RequestView struct {
	Request domain.FriendRequest
	Person  domain.AccountCard
}

// Overview is everything the friends page shows.
type Overview struct {
	Friends  []domain.AccountCard
	Incoming []RequestView
	Outgoing []RequestView
	Blocked  []domain.AccountCard
	Invites  []domain.Invite
	Now      time.Time
}

// Overview gathers the friends page in one call.
func (f *Friends) Overview(ctx context.Context, actor *domain.Account) (*Overview, error) {
	if actor == nil {
		return nil, domain.ErrUnauthenticated
	}
	out := &Overview{Now: f.deps.Clock.Now()}

	var err error
	if out.Friends, err = f.deps.Store.Friends(ctx, actor.ID); err != nil {
		return nil, fmt.Errorf("read friends: %w", err)
	}
	if out.Blocked, err = f.deps.Store.Blocks(ctx, actor.ID); err != nil {
		return nil, fmt.Errorf("read blocks: %w", err)
	}
	if out.Invites, err = f.deps.Store.InvitesForOwner(ctx, actor.ID); err != nil {
		return nil, fmt.Errorf("read invites: %w", err)
	}

	incoming, err := f.deps.Store.IncomingRequests(ctx, actor.ID)
	if err != nil {
		return nil, fmt.Errorf("read incoming requests: %w", err)
	}
	outgoing, err := f.deps.Store.OutgoingRequests(ctx, actor.ID)
	if err != nil {
		return nil, fmt.Errorf("read outgoing requests: %w", err)
	}

	ids := make([]domain.ID, 0, len(incoming)+len(outgoing))
	for _, r := range incoming {
		ids = append(ids, r.FromID)
	}
	for _, r := range outgoing {
		ids = append(ids, r.ToID)
	}
	cards, err := cardsFor(ctx, f.deps.Store, ids)
	if err != nil {
		return nil, err
	}

	for _, r := range incoming {
		out.Incoming = append(out.Incoming, RequestView{Request: r, Person: cards[r.FromID]})
	}
	for _, r := range outgoing {
		out.Outgoing = append(out.Outgoing, RequestView{Request: r, Person: cards[r.ToID]})
	}
	return out, nil
}
