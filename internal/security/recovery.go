package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Recovery codes are the answer to the question every second factor raises:
// what happens when the phone goes in the washing machine. Amici's answer is
// ten codes shown once, at enrolment, for the member to write down.
//
// They are shaped like invite codes and for the same reason: a person has to
// be able to copy one accurately off a piece of paper, possibly while upset
// about having lost their phone.
const (
	RecoveryCodeGroups   = 3
	RecoveryCodeGroupLen = 4
)

// NewRecoveryCode mints one code.
//
// The entropy is the same 30^12, about 2^58, as an invite code. That is not
// enough on its own to be safe against unlimited guessing, which is why the
// second factor challenge is rate limited and single-use, and why the codes
// are stored keyed rather than merely hashed.
func NewRecoveryCode() (string, error) {
	groups := make([]string, RecoveryCodeGroups)
	for g := range groups {
		var sb strings.Builder
		for i := 0; i < RecoveryCodeGroupLen; i++ {
			c, err := randomIndex(len(inviteAlphabet))
			if err != nil {
				return "", err
			}
			sb.WriteByte(inviteAlphabet[c])
		}
		groups[g] = sb.String()
	}
	return strings.Join(groups, "-"), nil
}

// NormaliseRecoveryCode is forgiving about how a code was copied out, while
// keeping the stored form canonical.
func NormaliseRecoveryCode(s string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(s))
	up = strings.NewReplacer(" ", "", "\t", "", "_", "", "-", "", "\u2013", "", "\u2014", "").Replace(up)
	if len(up) != RecoveryCodeGroups*RecoveryCodeGroupLen {
		return "", errors.New("that recovery code does not look right")
	}
	for _, r := range up {
		if !strings.ContainsRune(inviteAlphabet, r) {
			return "", errors.New("that recovery code contains characters we do not use")
		}
	}
	groups := make([]string, 0, RecoveryCodeGroups)
	for i := 0; i < len(up); i += RecoveryCodeGroupLen {
		groups = append(groups, up[i:i+RecoveryCodeGroupLen])
	}
	return strings.Join(groups, "-"), nil
}

// HashRecoveryCode derives the stored form, keyed with the application secret
// so that a database dump alone cannot be attacked offline.
func HashRecoveryCode(secret []byte, code string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("amici/recovery/v1"))
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}
