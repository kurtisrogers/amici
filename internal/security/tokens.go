package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SessionTokenBytes is the entropy in a session cookie. 32 bytes is far beyond
// brute force and keeps the cookie a comfortable length.
const SessionTokenBytes = 32

// NewSessionToken mints an opaque, URL-safe session secret.
func NewSessionToken() (string, error) { return randomToken(SessionTokenBytes) }

// HashToken derives the value stored in the database for a bearer secret.
//
// SHA-256 is the right tool here and Argon2 is not: the token already has 256
// bits of uniformly random entropy, so there is no dictionary to attack and
// nothing for a slow hash to buy us. What we need is a fast, constant-time
// lookup key that renders a stolen database useless for resuming sessions.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// InviteCodeGroups and InviteCodeGroupLen shape a friend request identifier
// into something a person can read down the phone to their mum:
// "amici-4F7K-Q2WD-9XRB".
const (
	InviteCodeGroups   = 3
	InviteCodeGroupLen = 4
)

// InviteCodePrefix is fixed so a code is recognisable when pasted somewhere.
const InviteCodePrefix = "amici"

// inviteAlphabet is Crockford-ish: no I, L, O, U, 0 or 1, so nothing is
// mistyped or misheard.
const inviteAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

// NewInviteCode mints a friend request identifier.
//
// Entropy: 30^12, about 2^58. Combined with a strict per-account rate limit on
// redemption attempts and a lifetime measured in hours, guessing one is not a
// practical attack.
func NewInviteCode() (string, error) {
	groups := make([]string, InviteCodeGroups)
	for g := range groups {
		var sb strings.Builder
		for i := 0; i < InviteCodeGroupLen; i++ {
			c, err := randomIndex(len(inviteAlphabet))
			if err != nil {
				return "", err
			}
			sb.WriteByte(inviteAlphabet[c])
		}
		groups[g] = sb.String()
	}
	return InviteCodePrefix + "-" + strings.Join(groups, "-"), nil
}

// NormaliseInviteCode makes redemption forgiving of how a code was pasted:
// case, spaces and missing or extra dashes all still work, while the stored
// form stays canonical.
func NormaliseInviteCode(s string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(s))
	up = strings.NewReplacer(" ", "", "\t", "", "_", "-", "\u2013", "-", "\u2014", "-").Replace(up)
	up = strings.TrimPrefix(up, strings.ToUpper(InviteCodePrefix)+"-")
	up = strings.ReplaceAll(up, "-", "")
	if len(up) != InviteCodeGroups*InviteCodeGroupLen {
		return "", errors.New("that request identifier does not look right")
	}
	for _, r := range up {
		if !strings.ContainsRune(inviteAlphabet, r) {
			return "", errors.New("that request identifier contains characters we do not use")
		}
	}
	groups := make([]string, 0, InviteCodeGroups)
	for i := 0; i < len(up); i += InviteCodeGroupLen {
		groups = append(groups, up[i:i+InviteCodeGroupLen])
	}
	return InviteCodePrefix + "-" + strings.Join(groups, "-"), nil
}

// HashInviteCode derives the stored form of an invite code. It is keyed with
// the application secret so that a database dump alone cannot be brute forced
// offline against the 2^58 code space.
func HashInviteCode(secret []byte, code string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("amici/invite/v1"))
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// CSRFTokenBytes is the entropy in a CSRF token.
const CSRFTokenBytes = 32

// NewCSRFToken mints a CSRF token.
func NewCSRFToken() (string, error) { return randomToken(CSRFTokenBytes) }

// ConstantTimeEqualString compares two strings without leaking their contents
// through timing.
func ConstantTimeEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// EqualHMAC compares two hex-encoded MACs safely.
func EqualHMAC(a, b string) bool {
	da, err1 := hex.DecodeString(a)
	db, err2 := hex.DecodeString(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return hmac.Equal(da, db)
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// randomIndex returns a uniform value in [0, n) using rejection sampling, so
// the invite alphabet is not subtly biased by a modulo.
func randomIndex(n int) (int, error) {
	if n <= 0 || n > 256 {
		return 0, fmt.Errorf("randomIndex: n out of range: %d", n)
	}
	limit := 256 - (256 % n)
	var b [1]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return 0, fmt.Errorf("read random byte: %w", err)
		}
		if int(b[0]) < limit {
			return int(b[0]) % n, nil
		}
	}
}
