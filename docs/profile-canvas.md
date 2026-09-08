# The profile canvas

Members can write the HTML and CSS of their own profile page. `<marquee>`
works. So do `<blink>`, `<center>`, `<font color="hotpink">`, table layouts
and as many gradients as anybody could want.

This is the single most dangerous feature in Amici. Accepting markup from one
member and rendering it in another member's browser is the textbook definition
of a stored cross-site scripting sink. What follows is how it is built and why
each part is there. If you are changing anything in
`internal/security/canvas/`, read this first.

## Five layers

Five independent things would all have to fail before a member's markup could
harm somebody who visits their page. No single one of them is trusted.

### 1. The Amici sanitiser

`internal/security/canvas` walks the parsed document with
`golang.org/x/net/html` and rebuilds it from an allowlist. Not a blocklist: an
allowlist. An element, attribute, URL scheme or CSS property that is not named
is dropped, and the member is told it was.

The element list is generous about decoration and silent about anything that
can execute, navigate on its own, fetch, or collect input. There is no
`script`, `iframe`, `object`, `embed`, `form`, `input`, `button`, `link`,
`meta`, `style`, `base`, `template`, `svg` or `math`. There *is* `marquee`,
`blink`, `center`, `font`, `big` and the whole table family, because the point
of the feature is that it should feel like 2004.

Attributes are filtered per element. No `on*` handlers survive, by construction
rather than by pattern matching: an attribute has to be in the allowlist to be
kept, and no event handler is in it. Structural limits cap depth, element
count, text run length and attribute value length, so one member's page cannot
be a denial of service against everyone who visits it.

### 2. A second, independent sanitiser

The output of layer 1 goes through `bluemonday` with an equally strict policy.

This is not belt-and-braces theatre. The realistic way a sanitiser fails is a
parsing quirk — some malformed input that our walker and the browser disagree
about. Two allowlist sanitisers written by different authors, applied in
series, means such a quirk has to exist in both, identically, to get through.

### 3. Rendered only in its own document

Canvas markup is never interpolated into an Amici page. It is served from
`GET /u/{handle}/canvas` as a standalone document and embedded in an iframe on
the profile. It shares no DOM with the page that carries the session.

That endpoint applies the same friendship check as the profile page, in the
service layer, so it is not a side door around it. A non-friend gets 404 —
which is what a browser spec asserts.

### 4. A sandboxed frame

The iframe is sandboxed *without* `allow-scripts` and *without*
`allow-same-origin`. Script cannot run, and the frame's origin is opaque, so
even a hypothetical script could not read Amici's cookies, reach its DOM, or
make a credentialed request back to it.

Those two flags are the load-bearing part of the whole feature. Adding either
of them back would undo it.

### 5. A Content Security Policy for the frame

The canvas document replaces the application's headers with its own, far
stricter, policy. It is documented directive by directive in
`internal/web/handlers_canvas.go`; the shape is `default-src 'none'` with
`script-src 'none'`, `img-src 'self' data:`, everything else `'none'`, and
`frame-ancestors 'self'` so nobody can embed a member's profile in their own
page.

The one permissive-looking directive is `style-src 'unsafe-inline'`, because
the member's CSS is inlined in a `style` element. In most contexts that
directive re-enables the injection CSP exists to prevent. Here the CSS has
already been through the allowlist, contains no `url()`, no escapes and no
unrecognised functions, and with `script-src 'none'` there is nothing for
injected CSS to escalate into.

## The CSS allowlist

`internal/security/canvas/css.go` is deliberately **not** a complete CSS
parser. A complete parser would have to reproduce every quirk of every
browser's error recovery, and any place where our understanding differs from
the browser's is a bypass. So it accepts a small, well-understood subset and
rejects everything it is not sure about.

Three rules do most of the work.

**No backslashes, anywhere.** CSS escapes are the standard way to smuggle a
banned token past a naive filter: `\75 rl(...)` is `url(...)`. Decorative CSS
never needs an escape, so banning the character outright removes the entire
obfuscation class rather than playing whack-a-mole with encodings.

**No `url()`.** Not "no external `url()`" — none at all. See below.

**Every function must be named.** `linear-gradient` and `calc` work;
`expression` and `image-set` do not.

Member selectors are scoped beneath `#amici-canvas`. Since the canvas is
already alone in its own document, scoping is not what makes it safe; it keeps
the stylesheet honest and means the same rendered CSS could be dropped into a
shared page later without leaking out of its box. Nesting depth, rule count,
declaration count and value length are all capped.

## Why no external resources at all

The easy version of this feature lets people hotlink a glittery background
from wherever they found it. Amici does not, and it is not laziness.

Every external URL in a profile is a request from the *viewer's* browser to a
stranger's server, carrying the viewer's IP address, the time they looked, and
usually enough to fingerprint them. Amici is built so that people cannot be
found. Handing an image host a log of everybody who visited a particular
profile would give that away for a tiled background.

So: no `url()` in CSS, no external `src` on images, no custom fonts, and
`img-src 'self' data:` to enforce it at the browser level even if the
sanitiser were wrong.

Instead Amici ships sixteen SVG stickers in `internal/web/static/stickers/`
(hearts, stars, lemons, a cat, a rainbow, and so on). They are local, so using
them costs a visitor nothing and reveals nothing. Members can also inline a
`data:` image up to 256 KB, and write as much gradient-soaked CSS as they
like.

## Telling the member what happened

When a save strips something, the member is told what and why, on the page,
every time — not behind a "show details" toggle. Somebody who pasted a widget
from elsewhere needs to know why half of it vanished, or they will reasonably
conclude Amici is broken.

The original source is always stored alongside the rendered output. Amici never
rewrites somebody's markup in place: the saved HTML and CSS are exactly what
they typed, and the sanitised version is a separate rendering of it. That
matters for two reasons. Editing your page shows you your own work rather than
our filtered version of it. And when the sanitiser gets stricter, old canvases
can be re-rendered from source.

## Versioning and re-rendering

`canvas.Version` identifies the sanitiser that produced a stored rendering.
When the rules change, bump it. The developer console has a re-render action
that finds every canvas rendered by an older version and puts its source
through the current sanitiser again.

This is why the source is kept. Without it, tightening a rule would only
protect canvases saved afterwards.

## Support, not censorship

Support can switch a member's profile back to plain rendering — the
`CapDisableCanvas` capability, used when a canvas is being used to harass or
impersonate. What that does is stop the rendered canvas being served. It does
not touch the markup.

Nobody at Amici edits somebody else's page. It either renders or it does not.
The member is told it has been switched off, and their source is still there,
still theirs, still editable. Every use of the action is written to the audit
trail against the name of the person who used it.

## Changing this code

- **Never** add `allow-scripts` or `allow-same-origin` to the frame sandbox.
- Adding an element or attribute means asking whether it can execute,
  navigate, fetch, submit, or collect input. If the answer to any of those is
  "maybe", leave it out.
- Adding a CSS property means asking whether any of its values can reference
  an external resource.
- Run the fuzz target. `go test -run '^$' -fuzz FuzzSanitiseNeverEmitsScript
  ./internal/security/canvas/`. CI runs it for a minute on every pull request;
  run it longer when you have changed the walker.
- Bump `canvas.Version` if the output of the sanitiser changes, and re-render.
