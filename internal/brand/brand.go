// Package brand holds Amici's identity: the name, the voice, and the
// colourways members choose between.
//
// It lives in its own package rather than in the templates because the
// colourway is validated on save, seeded by the fixtures, and rendered in
// three different places. One list, one source of truth.
package brand

import "fmt"

// Name is what we are called. Amici is Italian and Latin for "friends", which
// is the entire product specification in five letters.
const Name = "Amici"

// Tagline appears under the wordmark.
const Tagline = "A little corner of the internet for the people you love"

// Promise is the short version of why Amici is built the way it is. It is
// shown on the sign-in page, because a promise you hide is a promise you are
// planning to break.
const Promise = "No adverts. No algorithm. No strangers. No selling you."

// Colourway is a named palette. Each one is a full set of tokens rather than
// a single accent colour, so a member's choice actually changes the feel of
// the place instead of tinting one button.
//
// The names are all things you would find in an Italian kitchen or out of the
// window above it. They are meant to sound like a nice afternoon.
type Colourway struct {
	// Slug is the stored identifier and the value of the data-colourway
	// attribute on the document element.
	Slug string
	// Label is the name a member sees.
	Label string
	// Note is the one-line description in the picker.
	Note string
	// Swatch is the colour shown in the picker chip.
	Swatch string
}

// Colourways is the catalogue, in picker order.
var Colourways = []Colourway{
	{
		Slug:   "limonata",
		Label:  "Limonata",
		Note:   "Lemons on a bright kitchen table",
		Swatch: "#f2b705",
	},
	{
		Slug:   "fico",
		Label:  "Fico",
		Note:   "Ripe figs, warm and a bit purple",
		Swatch: "#8d5a97",
	},
	{
		Slug:   "cielo",
		Label:  "Cielo",
		Note:   "That first clear morning after the rain",
		Swatch: "#3fa7d6",
	},
	{
		Slug:   "pomodoro",
		Label:  "Pomodoro",
		Note:   "Tomatoes on the vine, Sunday sauce",
		Swatch: "#e05252",
	},
	{
		Slug:   "menta",
		Label:  "Menta",
		Note:   "Mint on the windowsill",
		Swatch: "#3aa981",
	},
	{
		Slug:   "notte",
		Label:  "Notte",
		Note:   "Late evening, everyone still talking",
		Swatch: "#5b6ee1",
	},
}

// DefaultColourway is what a new account starts with.
const DefaultColourway = "limonata"

// ValidateColourway checks a slug against the catalogue and returns it.
func ValidateColourway(slug string) (string, error) {
	for _, c := range Colourways {
		if c.Slug == slug {
			return c.Slug, nil
		}
	}
	return "", fmt.Errorf("%q is not one of our colourways", slug)
}

// ColourwayBySlug looks up a colourway, falling back to the default so a
// template never has to handle a missing palette.
func ColourwayBySlug(slug string) Colourway {
	for _, c := range Colourways {
		if c.Slug == slug {
			return c
		}
	}
	for _, c := range Colourways {
		if c.Slug == DefaultColourway {
			return c
		}
	}
	return Colourways[0]
}
