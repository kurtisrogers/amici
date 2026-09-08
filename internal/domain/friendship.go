package domain

import (
	"fmt"
	"strings"
	"time"
)

// Friendship is a mutual, symmetric connection. There is no "follow", no
// one-way subscription and no follower count, because those are the
// primitives that turn a group of friends into an audience.
//
// The pair is stored in a canonical order so that the database can enforce
// "at most one friendship between two people" with a primary key.
type Friendship struct {
	AccountA  ID // lexicographically smaller of the two
	AccountB  ID // lexicographically larger of the two
	CreatedAt time.Time
}

// FriendPair orders two account identifiers canonically.
func FriendPair(x, y ID) (ID, ID) {
	if x <= y {
		return x, y
	}
	return y, x
}

// RequestState is the lifecycle of a friend request.
type RequestState string

const (
	RequestPending   RequestState = "pending"
	RequestAccepted  RequestState = "accepted"
	RequestDeclined  RequestState = "declined"
	RequestCancelled RequestState = "cancelled"
)

// RequestOrigin records how the sender reached the recipient. Amici supports
// exactly two routes and this field is what makes that auditable.
type RequestOrigin string

const (
	// OriginEmail means the sender typed the recipient's email address, which
	// they had to already know.
	OriginEmail RequestOrigin = "email"
	// OriginInvite means the sender redeemed a request identifier that the
	// recipient generated and shared out of band.
	OriginInvite RequestOrigin = "invite"
)

// ParseRequestOrigin validates untrusted input.
func ParseRequestOrigin(s string) (RequestOrigin, error) {
	switch RequestOrigin(s) {
	case OriginEmail:
		return OriginEmail, nil
	case OriginInvite:
		return OriginInvite, nil
	default:
		return "", fmt.Errorf("%w: unknown request origin %q", ErrValidation, s)
	}
}

// FriendRequestNoteMaxLen caps the optional "it's me, from the choir" note.
const FriendRequestNoteMaxLen = 200

// FriendRequest is a pending or resolved request between two people.
type FriendRequest struct {
	ID          ID
	FromID      ID
	ToID        ID
	State       RequestState
	Origin      RequestOrigin
	Note        string
	CreatedAt   time.Time
	RespondedAt *time.Time
}

// ValidateFriendRequestNote trims and length-checks the optional note.
func ValidateFriendRequestNote(s string) (string, error) {
	n := strings.TrimSpace(s)
	if len([]rune(n)) > FriendRequestNoteMaxLen {
		return "", fmt.Errorf("%w: notes can be at most %d characters", ErrValidation, FriendRequestNoteMaxLen)
	}
	return n, nil
}

// Block prevents a person from reaching another by any route. A block also
// removes any existing friendship.
type Block struct {
	BlockerID ID
	BlockedID ID
	CreatedAt time.Time
}

// Invite is a friend request identifier: a single short-lived, single-use
// secret that its owner shares by whatever channel they already trust.
//
// The secret is never stored. Only a hash of it is kept, so a database dump
// cannot be replayed into someone's friend list.
type Invite struct {
	ID           ID
	OwnerID      ID
	CodeHash     string
	Label        string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	RedeemedAt   *time.Time
	RedeemedByID *ID
	RevokedAt    *time.Time
}

// InviteLabelMaxLen caps the private reminder note ("for Nan").
const InviteLabelMaxLen = 60

// ValidateInviteLabel trims and length-checks the owner's private label.
func ValidateInviteLabel(s string) (string, error) {
	l := strings.TrimSpace(s)
	if len([]rune(l)) > InviteLabelMaxLen {
		return "", fmt.Errorf("%w: labels can be at most %d characters", ErrValidation, InviteLabelMaxLen)
	}
	return l, nil
}

// Usable reports whether the invite can still be redeemed at the given time.
func (i *Invite) Usable(now time.Time) bool {
	return i.RedeemedAt == nil && i.RevokedAt == nil && now.Before(i.ExpiresAt)
}

// State summarises the invite for display without exposing the secret.
func (i *Invite) State(now time.Time) string {
	switch {
	case i.RevokedAt != nil:
		return "revoked"
	case i.RedeemedAt != nil:
		return "used"
	case !now.Before(i.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}
