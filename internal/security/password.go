// Package security holds the primitives Amici leans on for authentication and
// for making member-authored markup safe to serve.
package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters. These are the values Amici ships with; they are stored
// alongside each hash so that raising them later re-hashes on next sign-in
// rather than locking everyone out.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // 64 MiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

// Password length bounds. The minimum is a real minimum rather than a
// character-class puzzle: length beats "must contain a symbol" every time. The
// maximum exists so a multi-megabyte password cannot be used to exhaust the
// server through the hash function.
const (
	PasswordMinLen = 10
	PasswordMaxLen = 256
)

// ErrPasswordTooShort and ErrPasswordTooLong describe rejected passwords.
var (
	ErrPasswordTooShort = fmt.Errorf("passwords need to be at least %d characters, and a memorable phrase beats a clever word", PasswordMinLen)
	ErrPasswordTooLong  = fmt.Errorf("passwords can be at most %d characters", PasswordMaxLen)
	// ErrHashFormat means a stored hash could not be parsed.
	ErrHashFormat = errors.New("password hash is malformed")
)

// obviousPasswords are Amici-specific choices, folded into the embedded
// breach corpus at load. A corpus taken from other people's breaches cannot
// know that "amiciamici" is a bad idea here, because Amici has not had a
// breach to appear in.
var obviousPasswords = map[string]bool{
	"password":     true,
	"password1":    true,
	"password123":  true,
	"1234567890":   true,
	"qwertyuiop":   true,
	"iloveyou1":    true,
	"letmein123":   true,
	"amici":        true,
	"amiciamici":   true,
	"changeme":     true,
	"changeme123":  true,
	"welcome123":   true,
	"adminadmin":   true,
	"passwordpass": true,
}

// HashPassword derives an Argon2id hash with a fresh random salt. The result
// is a self-describing string in the standard PHC format.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read random salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a candidate against a stored hash in constant time.
// The second return value reports whether the hash used weaker parameters
// than we now require, so the caller can transparently upgrade it.
func VerifyPassword(hash, candidate string) (ok bool, needsRehash bool, err error) {
	if len(candidate) > PasswordMaxLen {
		// Refuse to spend 64 MiB of Argon2 work on an oversized guess.
		return false, false, nil
	}
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, false, ErrHashFormat
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, false, ErrHashFormat
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, false, ErrHashFormat
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, false, ErrHashFormat
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, false, ErrHashFormat
	}
	got := argon2.IDKey([]byte(candidate), salt, time, memory, threads, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, false, nil
	}
	weaker := memory < argonMemory || time < argonTime || len(want) < argonKeyLen
	return true, weaker, nil
}

// dummyHash is verified against when an email address has no account, so that
// a sign-in attempt for an unknown address costs the same wall-clock time as
// one for a known address. Without this, response timing is an account
// existence oracle, which on Amici is exactly the thing we must not leak.
var dummyHash string

func init() {
	h, err := HashPassword("amici-timing-equaliser-not-a-real-password")
	if err != nil {
		panic("amici: cannot build timing equaliser hash: " + err.Error())
	}
	dummyHash = h
}

// BurnPasswordTime performs the same work as a real verification and discards
// the result.
func BurnPasswordTime(candidate string) {
	_, _, _ = VerifyPassword(dummyHash, candidate)
}
