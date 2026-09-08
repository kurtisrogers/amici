// Package service holds Amici's business rules.
//
// Everything that decides who may see what lives here, not in the web layer
// and not in the store. Handlers parse and render; the store reads and writes;
// this package is where "only your friends can see your posts" is actually
// true. That separation is what lets the rules be tested without an HTTP
// server and reasoned about without reading templates.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/kurtisrogers/amici/internal/domain"
)

// Deps is everything the services need from the outside world.
type Deps struct {
	Store  domain.Store
	Clock  domain.Clock
	Logger *slog.Logger
	// Secret keys the invite code HMAC.
	Secret []byte
	// BaseURL is used to render the link that accompanies an invite code.
	BaseURL string
}

// Services is the assembled set, handed to the web layer as one value.
type Services struct {
	Accounts *Accounts
	Friends  *Friends
	Feed     *Feed
	Canvas   *Canvas
	Support  *Support
	Insights *Insights
}

// New wires the services together.
func New(d Deps) *Services {
	if d.Clock == nil {
		d.Clock = domain.SystemClock{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	limiter := newLimiter(d.Clock)
	return &Services{
		Accounts: &Accounts{deps: d, limiter: limiter},
		Friends:  &Friends{deps: d, limiter: limiter},
		Feed:     &Feed{deps: d, limiter: limiter},
		Canvas:   &Canvas{deps: d, limiter: limiter},
		Support:  &Support{deps: d, limiter: limiter},
		Insights: &Insights{deps: d},
	}
}

// audit writes an entry to the trail. A failure to record is logged loudly but
// does not fail the action: refusing to suspend an abusive account because the
// audit insert failed would be the wrong trade.
func (d Deps) audit(ctx context.Context, actor domain.ID, action, subject, detail string) {
	err := d.Store.AppendAudit(ctx, &domain.AuditEvent{
		ID:        domain.NewID(),
		ActorID:   actor,
		Action:    action,
		SubjectID: subject,
		Detail:    detail,
		CreatedAt: d.Clock.Now(),
	})
	if err != nil {
		d.Logger.Error("could not write audit event",
			slog.String("action", action),
			slog.String("actor", string(actor)),
			slog.String("error", err.Error()),
		)
	}
}

// requireCapability is the single gate for privileged actions.
func requireCapability(actor *domain.Account, c domain.Capability) error {
	if actor == nil {
		return fmt.Errorf("%w: sign in first", domain.ErrUnauthenticated)
	}
	if actor.Status != domain.StatusActive {
		return fmt.Errorf("%w: this account is not active", domain.ErrForbidden)
	}
	if !actor.Role.Can(c) {
		return fmt.Errorf("%w: your account cannot do that", domain.ErrForbidden)
	}
	return nil
}

// notFound is the answer Amici gives whenever telling someone "you are not
// allowed" would itself be the leak.
//
// On a network built so that people cannot be found, the difference between
// "this person does not exist" and "this person exists but you are not their
// friend" is exactly the fact we are protecting. So both are 404, and every
// call site uses this helper rather than deciding for itself.
func notFound(what string) error {
	return fmt.Errorf("%w: %s", domain.ErrNotFound, what)
}

// audienceFor returns the set of authors whose posts a viewer may see: their
// friends, plus themselves.
func audienceFor(ctx context.Context, store domain.FriendRepo, viewerID domain.ID) ([]domain.ID, error) {
	friends, err := store.FriendIDs(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("read friends: %w", err)
	}
	return append(friends, viewerID), nil
}

// canSeePost is the visibility rule, in one place.
func canSeePost(ctx context.Context, store domain.FriendRepo, viewerID domain.ID, post *domain.Post) (bool, error) {
	if post.AuthorID == viewerID {
		return true, nil
	}
	if post.Visibility == domain.VisibilityOnlyMe {
		return false, nil
	}
	blocked, err := store.BlockExistsEitherWay(ctx, viewerID, post.AuthorID)
	if err != nil {
		return false, fmt.Errorf("check block: %w", err)
	}
	if blocked {
		return false, nil
	}
	return store.AreFriends(ctx, viewerID, post.AuthorID)
}

// cardsFor resolves display cards for a set of accounts, tolerating accounts
// that have since been deleted so a feed never fails to render because
// somebody closed their account mid-request.
func cardsFor(ctx context.Context, store domain.AccountRepo, ids []domain.ID) (map[domain.ID]domain.AccountCard, error) {
	cards, err := store.AccountCards(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read account cards: %w", err)
	}
	for _, id := range ids {
		if _, ok := cards[id]; !ok {
			cards[id] = domain.AccountCard{
				ID:          id,
				Handle:      "someone",
				DisplayName: "Someone who has left",
				Colourway:   "limonata",
			}
		}
	}
	return cards, nil
}

// isConflict is a small readability helper used where a conflict is expected
// and handled rather than propagated.
func isConflict(err error) bool { return errors.Is(err, domain.ErrConflict) }
