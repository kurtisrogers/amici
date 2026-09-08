# Brand

## The name

**Amici** is Italian, from the Latin *amicus*: friends. It is the entire
product specification in five letters, and it says the thing the product is
about in a language that sounds like somebody's family rather than somebody's
startup.

The wordmark is the name in the brand font with a full stop rendered as a
coloured dot, which picks up whichever colourway the member has chosen. It is
a small thing that makes the place feel like theirs.

## The voice

Warm, plain, and never breathless. Amici talks like a person who is pleased to
see you and is not trying to sell you anything.

**Tagline:** *A little corner of the internet for the people you love.*

**Promise:** *No adverts. No algorithm. No strangers. No selling you.*

The promise is on the sign-in page, not buried in a policy, because a promise
you hide is a promise you are planning to break.

Rules that the copy actually follows:

- **British English**, and contractions are fine.
- **Say what happened, not what the system did.** "Your friends will see it
  next time they visit", not "Update successful".
- **Explain refusals.** When Amici says no, it says why, and where the limit
  came from. The friend request limit tells you it is a low limit on purpose
  and what it is protecting.
- **No growth language.** Nothing on Amici says "grow your network", suggests
  anybody, counts impressions, or nudges. There is no notification designed to
  bring somebody back.
- **No dark patterns.** No pre-ticked boxes, no "are you sure you want to miss
  out", no interstitials. Signing out is one click from every page.
- **Never pretend to be a person.** The service says "we".

## Colourways

Six palettes, named after things you would find in an Italian kitchen or out
of the window above it. Each is a full set of design tokens rather than a
single accent colour, so choosing one genuinely re-skins the whole interface
instead of tinting a button.

| Slug | Label | Note | Swatch |
| --- | --- | --- | --- |
| `limonata` | Limonata | Lemons on a bright kitchen table | `#f2b705` |
| `fico` | Fico | Ripe figs, warm and a bit purple | `#8d5a97` |
| `cielo` | Cielo | That first clear morning after the rain | `#3fa7d6` |
| `pomodoro` | Pomodoro | Tomatoes on the vine, Sunday sauce | `#e05252` |
| `menta` | Menta | Mint on the windowsill | `#3aa981` |
| `notte` | Notte | Late evening, everyone still talking | `#5b6ee1` |

`limonata` is the default, because it is the most cheerful thing in the set.

The catalogue lives in `internal/brand/brand.go`, in one list, because the
colourway is validated on save, seeded by the fixtures, and rendered in three
different places. Each palette overrides PicoCSS's own custom properties as
well as Amici's, which is why the re-skin is total.

A colourway is applied as `data-colourway` on the document element, so the
whole page switches with one attribute and no JavaScript. A browser spec
checks that changing it changes the whole place.

### Adding one

1. Add a `Colourway` to `brand.Colourways`.
2. Add a `[data-colourway="slug"]` block in `internal/web/static/amici.css`,
   setting the `--amici-*` tokens and the `--pico-primary*` overrides.
3. Add a `.amici-swatch__chip[data-colourway="slug"]` rule for the picker chip.

That is all. Validation, the picker and the fixtures read the catalogue.

## Stickers

Sixteen SVG stickers ship with Amici, in `internal/web/static/stickers/`:
heart, star, flower, sun, cloud, lemon, cherry, leaf, sparkle, rainbow, cat,
moon, coffee, cake, balloon, note.

They exist because members cannot reference external images — see
`docs/profile-canvas.md` for why — and a decoration feature with nothing to
decorate with is not a feature. They are local, so using one costs a visitor
nothing and tells nobody anything.

They are drawn flat and simple in a small palette so that any two look right
next to each other on the same page, whatever the member does with them.

## Typography and shape

Nunito where available, falling back to Trebuchet MS and then the system
stack. Rounded, friendly, and legible small. Nothing is loaded from a font
service: a custom font from a CDN is a request from every visitor's browser to
somebody else's server, which is exactly what Amici does not do.

Generous corner radii (14px on cards, 9px on controls), soft two-stage
shadows, and a two-tone wash background per colourway. Bubbly, but not
childish — this has to be somewhere a grandparent and a fifteen year old are
both comfortable.
