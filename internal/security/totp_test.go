package security

import (
	"strings"
	"testing"
	"time"
)

// The vectors in RFC 6238 appendix B, for the SHA-1 variant. If this test
// fails, every authenticator app in the world disagrees with us, so it is
// worth having even though the implementation is thirty lines.
func TestTOTPMatchesTheSpecification(t *testing.T) {
	// The RFC's secret is the ASCII "12345678901234567890", which is what its
	// vectors are computed from.
	secret := totpEncoding.EncodeToString([]byte("12345678901234567890"))

	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := TOTPCode(secret, time.Unix(c.unix, 0).UTC())
		if err != nil {
			t.Fatalf("code at %d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("code at %d = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestVerifyAcceptsTheNeighbouringWindows(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil {
		t.Fatalf("mint secret: %v", err)
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	// The window before, this one, and the next one all pass, because a phone
	// clock is never exactly right and people start typing late.
	for _, offset := range []time.Duration{-totpStep, 0, totpStep} {
		code, err := TOTPCode(secret, now.Add(offset))
		if err != nil {
			t.Fatalf("code: %v", err)
		}
		if !VerifyTOTP(secret, code, now) {
			t.Errorf("code from %v was refused", offset)
		}
	}

	// Two windows out does not.
	far, err := TOTPCode(secret, now.Add(3*totpStep))
	if err != nil {
		t.Fatalf("code: %v", err)
	}
	if VerifyTOTP(secret, far, now) {
		t.Error("a code three windows away was accepted")
	}
}

func TestVerifyToleratesHowPeopleTypeCodes(t *testing.T) {
	secret, _ := NewTOTPSecret()
	now := time.Now().UTC()
	code, _ := TOTPCode(secret, now)

	// Authenticator apps show codes as "123 456", and that space gets copied.
	spaced := code[:3] + " " + code[3:]
	if !VerifyTOTP(secret, spaced, now) {
		t.Error("a code with a space in it was refused")
	}
	// The stored secret is grouped for typing on the settings page; that form
	// has to keep working as a secret.
	if !VerifyTOTP(FormatTOTPSecret(secret), code, now) {
		t.Error("the grouped form of the secret was refused")
	}
}

func TestVerifyRefusesRubbish(t *testing.T) {
	secret, _ := NewTOTPSecret()
	now := time.Now().UTC()

	for _, code := range []string{"", "000000", "12345", "1234567", "abcdef", "      "} {
		if VerifyTOTP(secret, code, now) {
			// 000000 is a real code once in a million windows, so this would
			// be a flake if it ever fired for that reason. It has not.
			t.Errorf("%q was accepted", code)
		}
	}
	if VerifyTOTP("not-base32-at-all!!", "123456", now) {
		t.Error("a malformed secret accepted a code")
	}
}

func TestURIIsWhatAnAuthenticatorExpects(t *testing.T) {
	uri := TOTPURI("JBSWY3DPEHPK3PXP", "Amici", "sofia@example.invalid")

	for _, want := range []string{
		"otpauth://totp/",
		"secret=JBSWY3DPEHPK3PXP",
		"issuer=Amici",
		"digits=6",
		"period=30",
		"algorithm=SHA1",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI %q is missing %q", uri, want)
		}
	}
}

func TestRecoveryCodesRoundTripHowPeopleCopyThem(t *testing.T) {
	code, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("mint recovery code: %v", err)
	}
	canonical, err := NormaliseRecoveryCode(code)
	if err != nil {
		t.Fatalf("normalise %q: %v", code, err)
	}
	if canonical != code {
		t.Errorf("a freshly minted code %q is not canonical, got %q", code, canonical)
	}

	// Lower case, no dashes, spaces instead, an em dash from a word processor.
	for _, typed := range []string{
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		strings.ReplaceAll(code, "-", " "),
		strings.ReplaceAll(code, "-", "\u2014"),
		"  " + code + "  ",
	} {
		got, err := NormaliseRecoveryCode(typed)
		if err != nil {
			t.Errorf("normalise %q: %v", typed, err)
			continue
		}
		if got != canonical {
			t.Errorf("normalise %q = %q, want %q", typed, got, canonical)
		}
	}

	for _, bad := range []string{"", "too-short", "IIII-LLLL-OOOO", code + "-EXTRA"} {
		if _, err := NormaliseRecoveryCode(bad); err == nil {
			t.Errorf("%q was accepted as a recovery code", bad)
		}
	}
}

func TestRecoveryHashesAreKeyed(t *testing.T) {
	code, _ := NewRecoveryCode()
	a := HashRecoveryCode([]byte("secret-one-secret-one-secret-one"), code)
	b := HashRecoveryCode([]byte("secret-two-secret-two-secret-two"), code)
	if a == b {
		t.Error("the same code hashed the same under two different keys")
	}
	if a == HashRecoveryCode([]byte("secret-one-secret-one-secret-one"), code+"X") {
		t.Error("two different codes hashed the same")
	}
}
