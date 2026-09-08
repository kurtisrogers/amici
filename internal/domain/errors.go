package domain

import "errors"

// The sentinel errors below are the vocabulary the web layer translates into
// status codes and friendly messages. Services wrap them with context using
// fmt.Errorf("%w: ...") so that errors.Is keeps working.
var (
	// ErrValidation means the input was rejected before anything happened.
	ErrValidation = errors.New("validation")

	// ErrNotFound means the record does not exist, or the actor is not allowed
	// to know whether it exists. Amici prefers "not found" over "forbidden"
	// whenever telling the difference would leak the existence of a person or
	// a post.
	ErrNotFound = errors.New("not found")

	// ErrConflict means the change collided with existing state, such as a
	// handle already in use or a friend request already pending.
	ErrConflict = errors.New("conflict")

	// ErrForbidden means the actor is authenticated but lacks the capability.
	ErrForbidden = errors.New("forbidden")

	// ErrUnauthenticated means there is no valid session.
	ErrUnauthenticated = errors.New("unauthenticated")

	// ErrRateLimited means the actor is going too fast. Used heavily around
	// friend requests so the email route cannot be turned into a scanner.
	ErrRateLimited = errors.New("rate limited")

	// ErrCredentials means the email and password pair did not match. The
	// message is intentionally identical whether or not the account exists.
	ErrCredentials = errors.New("invalid credentials")

	// ErrExpired means a time-limited token is past its usable window.
	ErrExpired = errors.New("expired")
)
