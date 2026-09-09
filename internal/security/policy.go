package security

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Amici's password policy is length, a breach corpus, and a small number of
// structural checks. There is no "must contain a symbol", because that rule
// reliably produces Password1! and teaches people that security is a puzzle
// somebody else set them.
//
// The structural checks exist because a ten-character minimum invites a
// specific set of workarounds. Somebody who wants a short password and is told
// to type ten characters will repeat one ("aaaaaaaaaa"), repeat a word
// ("amiciamici"), walk a keyboard row ("qwertyuiop") or use their own name.
// All four are long, all four are in every cracking dictionary's generation
// rules, and none of them is caught by counting characters.

// keyboardSequences are the runs people walk when they need length and do not
// want to remember anything. Both directions are checked, so "0987654321" is
// covered by the ascending form.
var keyboardSequences = []string{
	"abcdefghijklmnopqrstuvwxyz",
	"01234567890",
	"qwertyuiop",
	"asdfghjkl",
	"zxcvbnm",
	"qwertzuiop",
	"azertyuiop",
	"1qaz2wsx3edc4rfv5tgb6yhn7ujm",
	"1q2w3e4r5t6y7u8i9o0p",
}

// minDistinctRunes is how much variety a password needs. Four distinct
// characters can still make a memorable phrase; three cannot make anything
// that is not a pattern.
const minDistinctRunes = 5

// ValidatePassword applies the policy with nothing known about the person.
func ValidatePassword(p string) error { return ValidatePasswordFor(p, nil) }

// ValidatePasswordFor applies the policy, additionally rejecting passwords
// made mostly of things an attacker already knows about the member: their
// handle, the words in their display name, the local part of their email
// address, and the name of this service.
//
// The test is "mostly", not "contains". A passphrase that happens to mention
// your own name is fine, and refusing it would be the kind of unexplainable
// rejection that sends people to a password manager's weakest suggestion. A
// password that is your name with a year after it is not fine, and the way to
// tell the two apart is to remove the known words and see whether anything
// substantial is left.
func ValidatePasswordFor(p string, personal []string) error {
	if utf8.RuneCountInString(p) < PasswordMinLen {
		return ErrPasswordTooShort
	}
	if len(p) > PasswordMaxLen {
		return ErrPasswordTooLong
	}

	folded := strings.ToLower(strings.TrimSpace(p))

	if IsBreachedPassword(folded) {
		return errors.New(
			"that password turns up in lists of passwords taken from other services, " +
				"so it is one of the first things anybody guessing would try. Please pick another")
	}
	if distinctRunes(folded) < minDistinctRunes {
		return fmt.Errorf(
			"that password uses too few different characters. A password needs at least %d, "+
				"and a short phrase you would remember is easier than it sounds", minDistinctRunes)
	}
	if unit, repeats := repeatedUnit(folded); repeats > 1 && len(unit)*2 <= PasswordMinLen {
		return errors.New(
			"that password is a short piece repeated to make up the length, which a guessing " +
				"program tries early. Please pick something with more to it")
	}
	if isKeyboardWalk(folded) {
		return errors.New(
			"that password is a run of neighbouring keys, which is one of the first patterns " +
				"anybody guessing would try. Please pick another")
	}
	if leftover, matched := stripPersonal(folded, personal); matched && utf8.RuneCountInString(leftover) < PasswordMinLen {
		return errors.New(
			"that password is mostly your own name or address, which is the first thing " +
				"somebody who knows you would try. Please pick something unrelated to you")
	}
	return nil
}

// PersonalTokensFor gathers the words about a member that a password should
// not be built out of.
func PersonalTokensFor(handle, displayName, email string) []string {
	tokens := []string{"amici"}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		// Below four characters this stops being a name and starts being a
		// syllable that appears in ordinary words.
		if utf8.RuneCountInString(s) >= 4 {
			tokens = append(tokens, s)
		}
	}

	add(handle)
	for _, word := range strings.FieldsFunc(displayName, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		add(word)
	}
	if at := strings.LastIndex(email, "@"); at > 0 {
		add(email[:at])
		if host := email[at+1:]; host != "" {
			// The domain's first label, so that somebody at example.com does
			// not choose "example" and their address.
			add(strings.SplitN(host, ".", 2)[0])
		}
	}
	return tokens
}

func distinctRunes(s string) int {
	seen := map[rune]struct{}{}
	for _, r := range s {
		seen[r] = struct{}{}
	}
	return len(seen)
}

// repeatedUnit finds the shortest string that, repeated, makes up the whole
// input, and how many times it repeats. For anything that is not a repetition
// it returns the input and one.
func repeatedUnit(s string) (string, int) {
	runes := []rune(s)
	n := len(runes)
	for unit := 1; unit <= n/2; unit++ {
		if n%unit != 0 {
			continue
		}
		matches := true
		for i := unit; i < n && matches; i++ {
			if runes[i] != runes[i%unit] {
				matches = false
			}
		}
		if matches {
			return string(runes[:unit]), n / unit
		}
	}
	return s, 1
}

// isKeyboardWalk reports whether the password is nothing but a run along a
// known sequence, in either direction.
func isKeyboardWalk(s string) bool {
	compact := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return r
		}
		return -1
	}, s)
	if utf8.RuneCountInString(compact) < PasswordMinLen {
		return false
	}
	for _, seq := range keyboardSequences {
		if strings.Contains(seq, compact) || strings.Contains(reverse(seq), compact) {
			return true
		}
	}
	return false
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// stripPersonal removes every known personal token from the password and
// reports whether any were found.
func stripPersonal(folded string, personal []string) (string, bool) {
	left, matched := folded, false
	for _, token := range personal {
		if token == "" || !strings.Contains(left, token) {
			continue
		}
		matched = true
		left = strings.ReplaceAll(left, token, "")
	}
	return left, matched
}
