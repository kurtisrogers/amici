package domain

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"
)

// ID identifies a record. Identifiers are random rather than sequential: a
// sequential id tells anyone who sees it how many accounts exist and lets them
// walk the set, which is exactly the kind of enumeration Amici exists to
// prevent.
type ID string

var idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// NewID mints a 128-bit identifier encoded in lowercase base32.
func NewID() ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing means the platform is unusable; there is no
		// safe way to continue handing out guessable identifiers.
		panic(fmt.Sprintf("amici: crypto/rand unavailable: %v", err))
	}
	return ID(idEncoding.EncodeToString(b[:]))
}

// Valid reports whether the identifier has the shape NewID produces. It is a
// cheap guard that keeps malformed path parameters out of the store.
func (id ID) Valid() bool {
	if len(id) != 26 {
		return false
	}
	for _, r := range id {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz234567", r) {
			return false
		}
	}
	return true
}

func (id ID) String() string { return string(id) }

// Date is a calendar date with no time or zone, used for birth dates where a
// timestamp would be both wrong and needlessly precise.
type Date struct {
	Year  int
	Month int
	Day   int
}

const dateLayout = "2006-01-02"

// ParseDate reads an ISO-8601 calendar date.
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(dateLayout, strings.TrimSpace(s))
	if err != nil {
		return Date{}, fmt.Errorf("%w: dates must look like 2006-01-02", ErrValidation)
	}
	return Date{Year: t.Year(), Month: int(t.Month()), Day: t.Day()}, nil
}

// String renders the date in ISO-8601.
func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day) }

// IsZero reports whether the date is unset.
func (d Date) IsZero() bool { return d.Year == 0 && d.Month == 0 && d.Day == 0 }

// AgeAt returns the age in whole years at the given instant.
func (d Date) AgeAt(t time.Time) int {
	if d.IsZero() {
		return 0
	}
	years := t.Year() - d.Year
	if int(t.Month()) < d.Month || (int(t.Month()) == d.Month && t.Day() < d.Day) {
		years--
	}
	if years < 0 {
		return 0
	}
	return years
}
