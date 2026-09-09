package domain

import (
	"fmt"
	"time"
)

// TokenPurpose says what a one-shot account token may be used for.
//
// The purpose is part of what is looked up, never inferred from context. A
// token minted to confirm an email address must not be redeemable as a
// password reset, and keeping the purpose in the row means that rule is
// enforced by the query rather than by remembering to check.
type TokenPurpose string

const (
	// PurposeEmailConfirm proves that whoever registered can read the address
	// they registered with.
	PurposeEmailConfirm TokenPurpose = "email_confirm"
	// PurposePasswordReset lets somebody who has lost their password set a
	// new one by proving they can read the account's email.
	PurposePasswordReset TokenPurpose = "password_reset"
	// PurposeEmailChange confirms a new address before it replaces the old
	// one.
	PurposeEmailChange TokenPurpose = "email_change"
	// PurposeTwoFactor is the short-lived handle on a sign-in that has passed
	// the password step and is waiting for a code. It is the only purpose
	// never sent by email.
	PurposeTwoFactor TokenPurpose = "two_factor"
)

// ParseTokenPurpose validates untrusted input.
func ParseTokenPurpose(s string) (TokenPurpose, error) {
	switch TokenPurpose(s) {
	case PurposeEmailConfirm:
		return PurposeEmailConfirm, nil
	case PurposePasswordReset:
		return PurposePasswordReset, nil
	case PurposeEmailChange:
		return PurposeEmailChange, nil
	case PurposeTwoFactor:
		return PurposeTwoFactor, nil
	default:
		return "", fmt.Errorf("%w: unknown token purpose %q", ErrValidation, s)
	}
}

// Token lifetimes.
//
// Each one is as short as the job allows. A confirmation link gets two days
// because people register in the evening and read their email at the weekend;
// a password reset gets an hour because it is the most valuable thing we ever
// put in an email; a sign-in waiting for a code gets ten minutes because the
// person is standing there holding their phone.
const (
	EmailConfirmTTL  = 48 * time.Hour
	PasswordResetTTL = time.Hour
	EmailChangeTTL   = 24 * time.Hour
	TwoFactorTTL     = 10 * time.Minute
)

// TTL returns how long a token for this purpose stays usable.
func (p TokenPurpose) TTL() time.Duration {
	switch p {
	case PurposeEmailConfirm:
		return EmailConfirmTTL
	case PurposePasswordReset:
		return PasswordResetTTL
	case PurposeEmailChange:
		return EmailChangeTTL
	case PurposeTwoFactor:
		return TwoFactorTTL
	default:
		return time.Hour
	}
}

// MaxTwoFactorAttempts bounds how many codes may be tried against one sign-in
// challenge. Six digits is a million possibilities, and a code is valid for
// about a minute, so a handful of guesses is generous and a thousand is an
// attack.
const MaxTwoFactorAttempts = 6

// AccountToken is a single-use secret tied to one account and one purpose.
//
// Only the hash is stored, in the same spirit as sessions and invite codes: a
// stolen copy of the database contains no usable password reset links.
type AccountToken struct {
	ID        ID
	AccountID ID
	Purpose   TokenPurpose
	TokenHash string
	// Email is the address the token was sent to. For a change of address it
	// is the new address, which is the whole point of confirming it; for the
	// others it is a record of where we sent it, so that a token minted before
	// an address changed cannot be redeemed afterwards.
	Email     string
	Attempts  int
	CreatedAt time.Time
	ExpiresAt time.Time
	// ConsumedAt is set when the token is spent. Spent tokens are kept until
	// they expire so that following the same link twice can say "that link has
	// already been used" instead of "that link is not valid", which sends
	// people round in circles.
	ConsumedAt *time.Time
}

// Usable reports whether the token may still be redeemed.
func (t *AccountToken) Usable(now time.Time) bool {
	return t.ConsumedAt == nil && now.Before(t.ExpiresAt)
}

// Spent reports whether the token was already used.
func (t *AccountToken) Spent() bool { return t.ConsumedAt != nil }

// RecoveryCode is one of the codes issued alongside a second factor, for the
// day the phone goes in the washing machine.
type RecoveryCode struct {
	ID        ID
	AccountID ID
	CodeHash  string
	CreatedAt time.Time
	UsedAt    *time.Time
}

// Used reports whether the code has been spent.
func (c *RecoveryCode) Used() bool { return c.UsedAt != nil }

// RecoveryCodeCount is how many codes are issued at once. Ten is enough that
// losing a few does not matter and few enough to write on one piece of paper.
const RecoveryCodeCount = 10
