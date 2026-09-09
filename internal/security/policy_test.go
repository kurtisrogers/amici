package security

import (
	"strings"
	"testing"
)

func TestTheCorpusIsActuallyLoaded(t *testing.T) {
	if n := CorpusSize(); n < 10000 {
		t.Fatalf("the embedded corpus holds %d entries, which suggests it did not load", n)
	}
	// A password long enough to pass the length rule and common enough to be
	// in any stuffing list. If this stops being caught, the corpus has been
	// filtered wrongly.
	if !IsBreachedPassword("password123") {
		t.Error("password123 is not in the corpus")
	}
	if IsBreachedPassword("scarlet-otter-brick-tuesday") {
		t.Error("an unlikely phrase was reported as breached")
	}
}

func TestLongButLazyPasswordsAreRefused(t *testing.T) {
	cases := map[string]string{
		"a repeated character":    "aaaaaaaaaa",
		"a repeated pair":         "ababababab",
		"a repeated word":         "amiciamici",
		"a keyboard row":          "qwertyuiop",
		"the alphabet":            "abcdefghij",
		"the digits":              "1234567890",
		"the digits backwards":    "0987654321",
		"a diagonal walk":         "1qaz2wsx3edc",
		"a known breached choice": "password123",
	}
	for name, password := range cases {
		if err := ValidatePassword(password); err == nil {
			t.Errorf("%s (%q) was accepted", name, password)
		}
	}
}

func TestMemorablePassphrasesAreAccepted(t *testing.T) {
	for _, password := range []string{
		"friends-and-family",
		"correct horse battery staple",
		"nonna makes the best ragu",
		"Tre limoni sul balcone",
	} {
		if err := ValidatePassword(password); err != nil {
			t.Errorf("%q was refused: %v", password, err)
		}
	}
}

func TestAPasswordMadeOfYourOwnNameIsRefused(t *testing.T) {
	personal := PersonalTokensFor("sofia", "Sofia Ricci", "sofia.ricci@example.invalid")

	for _, password := range []string{
		"sofiaricci1",
		"Sofia-Ricci-2026",
		"sofia.ricci@1",
		"example-sofia",
		"amici-sofia-1",
	} {
		if err := ValidatePasswordFor(password, personal); err == nil {
			t.Errorf("%q was accepted for Sofia", password)
		}
	}
}

// The rule is "mostly your own name", not "mentions it". A passphrase that
// happens to contain your name still has a passphrase's worth of material in
// it, and rejecting it would be the sort of refusal nobody can act on.
func TestAPassphraseMayMentionYourName(t *testing.T) {
	personal := PersonalTokensFor("sofia", "Sofia Ricci", "sofia.ricci@example.invalid")

	for _, password := range []string{
		"sofia loves the lemon tree",
		"tell ricci about the ragu on sunday",
	} {
		if err := ValidatePasswordFor(password, personal); err != nil {
			t.Errorf("%q was refused: %v", password, err)
		}
	}
}

func TestPersonalTokensSkipFragmentsTooShortToMeanAnything(t *testing.T) {
	got := PersonalTokensFor("bo", "Bo Li", "bo@x.example")

	for _, token := range got {
		if len(token) < 4 {
			t.Errorf("token %q is too short to be a name", token)
		}
	}
	// The service name is always in the set, whatever the member is called.
	if !contains(got, "amici") {
		t.Error("amici is missing from the personal tokens")
	}
}

func TestLengthBoundsStillApply(t *testing.T) {
	if err := ValidatePassword("short"); err != ErrPasswordTooShort {
		t.Errorf("a short password gave %v", err)
	}
	if err := ValidatePassword(strings.Repeat("a-longish-phrase ", 40)); err != ErrPasswordTooLong {
		t.Errorf("an oversized password gave %v", err)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
