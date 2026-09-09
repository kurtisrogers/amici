package security

import (
	"bufio"
	"embed"
	"strings"
	"sync"
)

//go:embed common-passwords.txt
var corpusFS embed.FS

// The breach corpus.
//
// docs/security.md used to record "no breach-corpus password check" as a known
// gap, and suggested a k-anonymity API as the fix. Amici does the check
// offline instead, and the reason is the same reason there are no third-party
// fonts or analytics: asking pwnedpasswords.com whether a member's password
// has been breached means telling somebody else, at the moment somebody
// registers, that a registration is happening. The prefix sent is only five
// characters of a SHA-1 and the practice is entirely respectable, but "we do
// not talk to anybody about our members" is a promise that is much easier to
// keep when there is no exception to explain.
//
// So the corpus is embedded. It holds the most common breached passwords that
// are at least PasswordMinLen characters long, which is the only part of a
// corpus that can reach us: everything shorter is already refused for being
// short. That filter is why twenty thousand entries is a meaningful list
// rather than a token one, since the overwhelming majority of the most-used
// passwords in any breach corpus are under ten characters.
//
// See common-passwords.txt for where the list comes from.
var (
	corpusOnce sync.Once
	corpus     map[string]struct{}
)

func loadCorpus() {
	f, err := corpusFS.Open("common-passwords.txt")
	if err != nil {
		// The file is embedded at compile time, so this cannot happen in a
		// built binary. An empty corpus is the safe failure: the length,
		// structural and personal checks all still apply.
		corpus = map[string]struct{}{}
		return
	}
	defer f.Close()

	corpus = make(map[string]struct{}, 20500)
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 4096)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		corpus[strings.ToLower(line)] = struct{}{}
	}
	for p := range obviousPasswords {
		corpus[p] = struct{}{}
	}
}

// IsBreachedPassword reports whether a password appears in the embedded
// corpus of commonly breached passwords.
//
// The comparison is case-insensitive. Capitalising the first letter of a
// breached password is the single most common way of "strengthening" one, and
// it does not help: any credential-stuffing list worth the name contains the
// obvious case variants already.
func IsBreachedPassword(p string) bool {
	corpusOnce.Do(loadCorpus)
	_, found := corpus[strings.ToLower(strings.TrimSpace(p))]
	return found
}

// CorpusSize reports how many entries the embedded corpus holds. The developer
// console shows it, so that whoever is running Amici can see the check is
// actually loaded rather than trusting that it is.
func CorpusSize() int {
	corpusOnce.Do(loadCorpus)
	return len(corpus)
}
