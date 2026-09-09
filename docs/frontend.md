# Front end

Server-rendered HTML, PicoCSS, and no JavaScript. That last part is not a
purity exercise, and it is not laziness either.

## Why no JavaScript

Three reasons, in order of how much they mattered.

**It has to work on a bad phone.** A social network for families is read on
whatever handset is in the house, often on a poor connection. Every page Amici
serves is complete HTML that renders as it arrives.

**It shrinks the canvas attack surface.** Amici accepts HTML from members and
shows it to their friends. The less script there is on the origin, the less
there is for an escaped canvas to reach. It also means a Content Security
Policy of `script-src 'self'` costs nothing, because there is nothing to load.

**It keeps the codebase one thing.** No build step, no bundler, no lockfile
outside `e2e/`, no second language for state management. `go build` produces
the whole application.

A browser spec asserts this holds: with JavaScript disabled in the browser
context, a member can still sign in, post, react and comment.

The cost is that some interactions take a round trip that could have been
local — reacting to a post is a form submission and a redirect. On a family
network with a handful of posts a day, that is a trade worth making, and
post-redirect-get means a refresh never double-posts.

## PicoCSS

Vendored at `internal/web/static/pico.min.css`. Not from a CDN, because a CDN
sees every request every member makes, which is precisely what Amici is built
not to allow.

Pico does the heavy lifting: sensible typography, forms that already work
without a single class, and a light and dark scheme. `amici.css` on top of it
is the brand layer — the colourways plus the components Pico has no opinion
about (post cards, people lists, reaction rows, swatch chips). Keeping it to
those two files means the styling is something a person can read in one
sitting.

Amici leans on Pico's semantic styling, so most markup carries no classes at
all. A `<form>` with labels and inputs looks right as written.

## Templates

`html/template`, embedded with `go:embed`, parsed once at startup.

The shape is a `head`/`foot` pair rather than a single layout template with
blocks, because Go's template inheritance is awkward and this is legible:

```
{{template "head" .}}
  ... the page ...
{{template "foot" .}}
```

Shared partials live in `partials.html`: `head`, `foot`, `csrf`, `flash`,
`avatar`, `avatarSmall`, `post`, `composer`.

Every page is handed one `page` struct with `Viewer`, `Brand`, `CSRF`, `Flash`,
`Now` and a `Data` field for the page's own payload. Handlers define a small
struct per page for `Data`, so what a template can reach is visible in Go
rather than discovered by reading templates.

Where a partial needs several pieces of context, an `item` helper bundles them
into an `itemView` (`internal/web/view.go`) rather than the template reaching
around with `$`. Template helper functions are registered in `render.go`: `timeAgo`,
`niceTime`, `niceDate`, `duration`, `pluralise`, `paragraphs`, `bytes`, `add`,
`item`, `stickerPath`, `reactionKinds`, `visibilities`, `canvasHTML` and
`canvasCSS`.

Two gotchas worth knowing, both of which have already bitten this codebase:

- `{{with}}` cannot be followed by `{{else if}}`. Use `{{if}}` when you need
  the chain.
- Inside `{{range}}` and `{{with}}`, `.` is rebound. Use `$` for the page.

## Accessibility

What is done:

- Semantic HTML throughout: real headings, real lists, real form labels, real
  `<article>` per post.
- Every input has a `<label>` with a `for`, not a placeholder standing in for
  one. Placeholders are examples, not labels.
- Flash messages use `role="alert"`.
- Reaction buttons carry `aria-pressed`, so their state is announced rather
  than only coloured.
- Accessible names say what they act on. The report control on a post says
  "Report this post", not "Report", because a feed is a list of them and
  "Report" a dozen times over tells a screen reader user nothing.
- Colourways keep text contrast within a palette rather than only changing the
  accent.
- The whole application is keyboard reachable, which follows from it being
  forms and links.

- Links are announced as links. This used to be a recorded wart: PicoCSS
  styles a call-to-action anchor by putting `role="button"` on it, and Amici
  followed that convention, so a link that navigates was announced as a button
  and keyboard users were implicitly told Space would activate it when only
  Enter does.

  It is fixed by reimplementing the styling behind a class instead of a lie
  about the role. `a.amici-cta` in `amici.css` gives an anchor the same
  appearance Pico gives a button, with `--quiet` and `--small` variants
  standing in for Pico's `.secondary` and `.outline`, which are only defined
  for button-ish selectors. The markup is now an ordinary `<a href>` with a
  class, and the browser specs query these controls by the link role, because
  that is what they are.

What is not done yet is in `docs/roadmap.md`: there has been no screen reader
pass and no automated audit in CI, so what is above is a description of intent
plus the checks the specs happen to make, not a claim of conformance to a
level.

## Adding a page

1. A handler in `internal/web/handlers_*.go` with a small `Data` struct.
2. A route in `routes.go`, under the group matching who may reach it —
   `open`, `member`, or `gated`.
3. A template in `internal/web/templates/`, opening with `head` and closing
   with `foot`.
4. Any new styling in `amici.css`, using the `--amici-*` tokens so it follows
   the colourways.

No build step. Restart the server and reload.
