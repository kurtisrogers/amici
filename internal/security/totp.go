package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Amici implements RFC 6238 directly rather than taking a dependency.
//
// The algorithm is a HMAC of a counter, truncated to six digits, and the
// specification fits on two pages. Writing it here keeps the second factor
// auditable in the same place as the password hashing, and means the set of
// third-party code that can reach an authentication path stays as small as it
// is everywhere else in this codebase.
const (
	// totpStep is the window length every authenticator app assumes.
	totpStep = 30 * time.Second
	// totpDigits is what people expect to type.
	totpDigits = 6
	// totpSkew is how many steps either side of now are accepted, which
	// covers a phone clock that is a little out and a person who starts
	// typing as the code is about to roll over.
	totpSkew = 1
	// totpSecretBytes is the shared secret length. RFC 4226 asks for at least
	// 128 bits and recommends 160, which is also exactly what SHA-1's block
	// structure wants.
	totpSecretBytes = 20
)

// totpEncoding is unpadded base32, which is what the otpauth URI scheme and
// every authenticator app expect.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrTOTPSecret means a stored secret could not be decoded.
var ErrTOTPSecret = errors.New("second factor secret is malformed")

// NewTOTPSecret mints a shared secret in the base32 form apps accept.
func NewTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random secret: %w", err)
	}
	return totpEncoding.EncodeToString(b), nil
}

// TOTPCode computes the code for a secret at an instant.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := totpEncoding.DecodeString(normaliseSecret(secret))
	if err != nil {
		return "", ErrTOTPSecret
	}
	return hotp(key, uint64(at.UTC().Unix())/uint64(totpStep.Seconds())), nil
}

// VerifyTOTP checks a code against a secret, accepting the neighbouring steps.
//
// The comparison is constant time, and every candidate step is computed even
// once one has matched. Returning as soon as a step matches would leak, in
// timing, whether the code was early, on time or late, which is a small thing
// but a free one to avoid.
//
// A code stays valid for the length of its window, so the same code could in
// principle be presented twice. What stops that being useful is the layer
// above: a code is only ever accepted against a single-use challenge that is
// consumed on success, and getting another challenge means typing the password
// again.
func VerifyTOTP(secret, code string, now time.Time) bool {
	candidate := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
	if len(candidate) != totpDigits {
		return false
	}
	key, err := totpEncoding.DecodeString(normaliseSecret(secret))
	if err != nil || len(key) == 0 {
		return false
	}

	counter := uint64(now.UTC().Unix()) / uint64(totpStep.Seconds())
	matched := 0
	for offset := -totpSkew; offset <= totpSkew; offset++ {
		step := int64(counter) + int64(offset)
		if step < 0 {
			continue
		}
		want := hotp(key, uint64(step))
		matched |= subtle.ConstantTimeCompare([]byte(want), []byte(candidate))
	}
	return matched == 1
}

// hotp is the RFC 4226 truncation of a HMAC over a counter.
func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)

	// The low four bits of the last byte select where in the digest to read
	// the four-byte dynamic binary code from.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod)
}

// TOTPURI builds the otpauth URI an authenticator app reads.
//
// Amici shows this as text rather than as a QR code. A QR code would mean
// either shipping an encoder and rendering an image, or asking a third party
// to draw it, and the second of those would hand somebody else a shared
// secret. Typing a secret once is a fair price for not doing that, and a
// server-side encoder is queued in docs/roadmap.md.
func TOTPURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", normaliseSecret(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(int(totpStep.Seconds())))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// FormatTOTPSecret groups a secret into fours so it can be typed off a screen
// without losing your place.
func FormatTOTPSecret(secret string) string {
	s := normaliseSecret(secret)
	var groups []string
	for i := 0; i < len(s); i += 4 {
		end := i + 4
		if end > len(s) {
			end = len(s)
		}
		groups = append(groups, s[i:end])
	}
	return strings.Join(groups, " ")
}

func normaliseSecret(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}
