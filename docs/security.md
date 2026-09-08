# Security and privacy

Amici's threat model is not "a nation state wants this database". It is the
ordinary, everyday harm that social networks do: being found by somebody you
left, having your child reachable by a stranger, having what you read logged
and sold, having a photograph of your kitchen indexed by a search engine.

So the controls below are weighted towards *not knowing things* and *not being
findable*, and only then towards the usual web application hardening.

The profile canvas has its own document, `docs/profile-canvas.md`, because it
is the one genuinely dangerous feature and deserves the space.

## Privacy by construction

**There is no search.** No `/search`, no `/discover`, no `/people`, no
`/suggestions`. Read `internal/web/routes.go` and the absence is visible in one
screen. This is not a feature that was descoped; it is the product.

**Two ways to reach somebody, and no third.** An email address you already
knew, or a short-lived code they handed you. Nothing else. No mutual-friend
suggestions, no contact upload, no "people you may know", no follow.

**Visibility has two values.** `friends` and `only_me`. The `Visibility` type
cannot represent a public post, so there is no code path that could
accidentally publish one.

**A profile a viewer may not see answers 404, not 403.** On a network built so
that people cannot be found, "this person exists but you are not their friend"
is exactly the fact being protected. Every such answer goes through one helper
so no call site can decide differently. A handle nobody registered and a
stranger's profile are indistinguishable — a browser spec asserts it.

**The email route says nothing at all.** Sending a friend request to an
address answers identically whether the address belongs to nobody, to somebody
who has switched off email requests, to a young member who cannot be reached
that way, to somebody who blocked you, or to a friend you already have. All
five paths return the same sentence. `RequestByEmail` returns no indication of
what it did, by design, and both a service test and a browser test compare the
five responses.

**Rate limits are part of the privacy model, not just anti-abuse.** A perfectly
silent response is still an oracle if you can ask ten thousand times and watch
which addresses later appear as friends. Friend requests by email are capped
per account per hour and per account per day, and the daily count is kept in
the database so a restart does not hand anybody a fresh budget.

**Logs are deliberately thin.** One line per request: request id, method,
path, status, duration. No query string, no user agent, no referrer, no
identifier for who was signed in. A log line is data, data leaks, and Amici
does not need to know which member read which profile in order to run.

**Nothing loads from a third party.** No CDN, no font service, no analytics, no
error reporting. PicoCSS is vendored. A member cannot reference an external
image, for the reasons in `docs/profile-canvas.md`.

**There are no photographs.** An avatar is the member's initials on their
colourway, and there is no upload anywhere in the application. A photo of a
child's face is the single most sensitive thing a social network can hold, and
the safest way to hold it is not to. It also means Amici stores no binary blobs
and needs no image pipeline, which removes a whole class of parser
vulnerability along with the privacy problem.

**Crawlers are turned away three ways.** `X-Robots-Tag` on *every* response,
not just the pages somebody remembered; a `robots.txt` that disallows
everything and names the known AI training crawlers individually; and, the part
that actually matters, a session and a friendship required for every page worth
crawling, so a crawler that ignores both gets a sign-in form.

## Protecting children

- Registration refuses anybody under 13 (`domain.MinimumAgeYears`).
- Under 18, the email route is **never** available, whatever the member's
  settings say. Knowing a child's email address does not entitle you to reach
  them. This is enforced in `deliver`, so both routes go through it.
- Under 18, invite codes expire in 4 hours rather than 24.
- A young member's account is flagged in the support console, so somebody
  helping them knows before they act.

There is no separate "child account" type. A young member gets the same
product with stricter reachability, because a visibly different account is
itself a signal to anybody looking.

## Accounts and roles

Three roles: `member`, `support`, `developer`. There is no "page", "brand" or
"advertiser" role, and there never will be.

Authorisation is by **capability**, not by role check. Services ask "may this
actor do this?" rather than "is this actor an admin?", so a new role can be
introduced without auditing every call site. The capability sets are in
`internal/domain/account.go`.

The important part is what support and developers *cannot* do. Neither role
holds any capability that reads member content. Support can resolve an account
from an email address or handle, see counts and status, suspend and restore,
act on reports, and switch a canvas to plain rendering. Support cannot read a
post, a comment, or a friend list — there is no code path from the console to
any of it. Developers see diagnostics and the audit trail and no member content
at all. Two service tests and two browser tests hold that line.

Every privileged action is written to an audit trail against the name of the
person who did it, and support is told on the page that this is happening.

## Authentication

**Passwords** are hashed with Argon2id (64 MiB, 3 iterations, 4 threads, 16-byte
salt, 32-byte key). Parameters are stored alongside each hash, so raising them
later re-hashes on next sign-in rather than locking everybody out. Minimum
length 10, maximum 256 — the maximum exists so a multi-megabyte password cannot
exhaust the server through the hash function. There are no character-class
rules, because length beats "must contain a symbol", and a small list of the
very worst choices is rejected outright.

**Sessions** are 32 random bytes in a cookie; only a SHA-256 hash is stored.
SHA-256 rather than Argon2 is correct here: the token already has 256 bits of
uniform entropy, so there is no dictionary to attack, and what is needed is a
fast constant-time lookup key that renders a stolen database useless for
resuming sessions.

Cookies are `HttpOnly`, `Secure` in production (enforced — the config refuses
to start production without it), `SameSite=Lax`, and use the `__Host-` prefix
when Secure. That prefix is a browser-enforced guarantee that the cookie has no
`Domain` attribute, which means a compromised sibling subdomain cannot set or
overwrite the session cookie.

`SameSite=Lax` rather than `Strict` is a deliberate trade: Strict would mean
that following a link somebody sent you to a post lands you on a sign-in page
even though you are signed in, which is a poor experience for a network whose
whole purpose is people sending each other things. Lax still withholds the
cookie from cross-site POSTs, and the CSRF token covers state-changing requests
regardless.

Changing a password closes every other session. A browser spec checks it with
two browser contexts.

**Failed sign-ins** are counted, successful ones are not. Counting successes
would rate-limit a household: a family behind one home connection is a single
client address, and a mobile network puts thousands of unrelated people behind
a handful. Credential guessing is made of failures, so failures are what is
counted — ten per email address and thirty per client address in fifteen
minutes. A success clears that account's budget but not the client's, because
clearing the client's would let somebody with one valid account of their own
wipe the counter between guesses at everybody else's.

While an account is locked out, the *correct* password is refused too.
Otherwise the lockout becomes a way to test passwords rather than a stop to it.

## Friend request codes

Format `amici-4F7K-Q2WD-9XRB`: three groups of four from a 30-character
alphabet with no I, L, O, U, 0 or 1, so nothing is mistyped or misheard down
the phone. That is about 2^58 of entropy.

Codes are **never stored**. Only an HMAC-SHA256 of the code, keyed with the
application secret, so a database dump alone cannot be brute-forced offline
against the code space. This is why the member is told to copy their code
immediately: nobody at Amici can show it to them again, including us.

Single use, and expiring — 24 hours, or 4 for a young member. Redemption
attempts are rate limited per account and per client. An account may have eight
codes live at once: enough for a family gathering, few enough that codes are
not sprayed around.

Redeeming a code creates a *request*, not a friendship. A code can be
forwarded, so the owner still gets the last word about who ends up in their
life. A browser spec asserts that redeeming does not grant access.

## CSRF

Double-submit cookie plus an origin check, on every unsafe method.

The token comparison is the primary defence: a cross-site attacker can make a
browser send our cookie but cannot read it, so cannot put a matching value in a
form field. The origin check is the backstop.

Origin verification prefers `Sec-Fetch-Site`, which is the browser's own answer
to the question being asked and cannot be set by page script. `same-origin` and
`none` pass; `same-site` and `cross-site` are refused, since Amici has no
sibling subdomains. Without that header it falls back to `Origin`, then
`Referer`, compared against both the request host and the configured base URL
so a reverse proxy that rewrites Host does not break it. An `Origin` of the
literal string `null` — an opaque origin, which is what a sandboxed frame sends
— is refused explicitly, and that is the case that matters, because the profile
canvas runs in exactly such a frame.

A request with neither header is allowed through the origin check, because that
is what a stripped-down script sends rather than a browser doing a cross-site
form post, and the token still has to match.

`Referrer-Policy` is `same-origin`, not `no-referrer`. Both send nothing to a
third party, which is the privacy goal, but `no-referrer` also makes browsers
serialise the `Origin` header on our *own* form posts as `null`, because Origin
is derived from the referrer policy. That leaves the origin check nothing
truthful to compare and rejects every form on the site.

## Response headers

Set on every response by one middleware, so there is no page somebody forgot.

| Header | Value | Why |
| --- | --- | --- |
| `Content-Security-Policy` | `default-src 'none'` plus `'self'` for style, script, img, font, form-action, frame-src | No third-party anything |
| | `frame-ancestors 'none'` | Nobody can frame Amici, which rules out clickjacking a member into clicking Accept |
| | `base-uri 'none'` | No rewriting where relative URLs point |
| `X-Content-Type-Options` | `nosniff` | |
| `X-Frame-Options` | `DENY` | For anything that predates `frame-ancestors` |
| `Referrer-Policy` | `same-origin` | See above |
| `Permissions-Policy` | camera, microphone, geolocation and the rest denied, plus `interest-cohort` and `browsing-topics` | The quietest permission prompt is the one never asked. The last two opt out of ad-topic inference |
| `Cross-Origin-Opener-Policy` | `same-origin` | |
| `Cross-Origin-Resource-Policy` | `same-origin` | |
| `X-Robots-Tag` | `noindex, nofollow, noarchive, nosnippet, noimageindex, notranslate` | |
| `Strict-Transport-Security` | 2 years, `includeSubDomains`, `preload` | Only when already on HTTPS, so a development server cannot poison a developer's browser for the whole localhost origin |

The canvas frame replaces these with its own stricter set. See
`docs/profile-canvas.md`.

## Other hardening

- **Request bodies are bounded** (512 KB by default) before any parsing.
- **Panics** become a 500 with the request id logged, not a dropped
  connection.
- **Redirect targets** are validated: anything absolute, protocol-relative or
  backslash-prefixed is discarded, so `?next=https://evil.example` cannot turn
  the sign-in page into a phishing launch pad.
- **SQL** is parameterised throughout. No string concatenation into a query.
- **Templates** use `html/template`, so contextual escaping is automatic. The
  only place member markup is rendered as markup is the canvas frame.
- **Timing.** A sign-in for an address with no account still spends the cost of
  a password verification, so response time does not reveal whether an address
  is registered.
- **Identifiers** are 128 random bits in base32, so no URL reveals how many
  members there are or lets anybody walk the set.
- **`/healthz`** returns `ok` and nothing else. A health endpoint that reports
  version and database state is a reconnaissance endpoint.
- **`/.well-known/security.txt`** tells a researcher where to send a finding.
  Change the address before running this for real.

## Fixtures cannot ship

`/fixtures/reset` wipes the database and rebuilds a known cast. It exists
behind two independent locks: the route is only registered when
`AMICI_ENABLE_FIXTURES` is set, and `config.Load` refuses to start at all if
that flag is set while `AMICI_ENV=production`. Not one flag checked inside a
handler.

Note also what fixtures do *not* relax: CSRF still applies to the reset
endpoint, and the browser suite fetches a token like a browser would. An
endpoint that only behaves in tests is not the endpoint being tested.

## Known gaps

Written down rather than left to be discovered.

- **No email delivery.** Registration does not confirm the address, which
  means somebody could register with an address they do not control and, since
  the email route is how people reach each other, receive requests intended for
  its real owner. This is the most important gap. Confirming the address at
  registration closes it and is the obvious next job.
- **No password reset**, which follows from the above.
- **No second factor.**
- **The rate limiter is in-process.** Two instances would each keep their own
  counters. The limit that matters most, friend requests per day, is counted in
  the database and holds regardless.
- **No breach-corpus password check.** Only a short list of the worst choices
  is rejected. A proper check belongs behind a k-anonymity API.
- **Client-address rate limiting is a blunt instrument** where carrier NAT is
  involved. The per-account limits are the ones doing the real work; the
  per-client ones are ceilings on a single machine, pitched well above what any
  household sends.
- **No at-rest encryption.** The SQLite file is plain. Encrypt the volume.
