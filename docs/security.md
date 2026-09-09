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
per account per hour and per account per day, and every counter is kept in the
database so a restart does not hand anybody a fresh budget.

Where a limit guards a page that is deliberately uninformative, the *limit
itself* has to be uninformative too. The password reset form charges its
client budget before it looks the address up, so an address with an account
behind it costs exactly what an unknown one costs. If it did not, the point at
which the form started refusing would answer the question the page exists to
avoid answering.

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
exhaust the server through the hash function.

There are no character-class rules, because that rule reliably produces
`Password1!` and teaches people that security is a puzzle somebody else set
them. What is checked instead:

- **A breach corpus**, embedded in the binary. It holds the most commonly
  breached passwords that are at least ten characters long, which is the only
  part of any corpus that can reach us, since everything shorter is already
  refused for being short. The comparison is case-insensitive, because
  capitalising the first letter of a breached password is the most common way
  of "strengthening" one and any credential-stuffing list worth the name
  contains the obvious case variants already.

  This is offline on purpose. A k-anonymity API such as
  `pwnedpasswords.com` is entirely respectable and sends only five characters
  of a SHA-1, but it also means telling somebody else, at the moment somebody
  registers, that a registration is happening. "We do not talk to anybody
  about our members" is a promise that is much easier to keep when there is no
  exception to explain. See `internal/security/common-passwords.md` for where
  the list comes from and how to refresh it.

- **Structural patterns.** A ten-character minimum invites a specific set of
  workarounds: repeat one character, repeat a word, walk a keyboard row. All
  three are long, all three are in every cracking dictionary's generation
  rules, and none is caught by counting characters.

- **Things an attacker already knows about you** — your handle, the words in
  your display name, the local part of your email address, and the name of
  this service. The test is "mostly", not "contains": a passphrase that
  happens to mention your own name is fine and refusing it would be the kind
  of unexplainable rejection that sends people to a password manager's weakest
  suggestion. A password that is your name with a year after it is not fine.

**Email addresses are confirmed.** A newly registered address is unproved, and
an unproved address cannot be used to reach the member and cannot receive a
password reset. This closes what used to be the most serious gap in Amici:
because the email route is how people find each other here, registering with
an address you do not control was a quiet way to receive the friend requests
meant for its real owner.

It is a banner rather than a wall. The only thing an unconfirmed address costs
is being reachable by email, so a member who never wants to be reachable that
way can ignore the reminder forever and nothing stops working — their request
codes are unaffected.

**Password reset** sends a single-use link that lasts an hour. Every answer
the form gives is the same one, whether the address belongs to nobody, to a
suspended account, to a closed account, or to an account that never confirmed
its address. Setting a new password closes every session, including the
session of whoever asked for the reset.

The link is checked but not spent when the page loads, and spent only when a
new password is actually accepted. Both halves matter: mail providers fetch
links before anybody reads them, so spending on the GET would let a scanner
burn the member's only way back in, and spending before validation would mean
a password we refuse costs somebody their link rather than another attempt.

**Single-use tokens** — confirmation, reset, address change, and the handle on
a sign-in waiting for a code — all live in one table, and the purpose is part
of the lookup rather than something a caller remembers to check afterwards. A
confirmation link arrives at an address nobody has yet proved they can read,
so a confirmation link redeemable as a password reset would be a complete
account takeover. The difference between those two is one `WHERE` clause, and
it is in the query rather than in a handler.

**A second factor** is a time-based code from an authenticator app, and
nothing else. RFC 6238 is two pages and is implemented directly in
`internal/security/totp.go` rather than taken as a dependency, which keeps the
set of third-party code that can reach an authentication path as small as it
is everywhere else here.

There is no SMS option, and that is a decision rather than an omission. A text
message means handing a phone number to a network and a gateway provider, and
a phone number is the most re-identifiable thing a person can give you. It is
also the weakest common second factor, because it can be taken from somebody
by talking to their mobile operator.

Enrolment is two steps: the first mints a secret and shows it, the second
waits for a correct code before recording that the factor is on. Without that
ordering, somebody who opened the settings page and wandered off would have
locked themselves out of their own account with a secret they never stored.

Ten recovery codes are issued at enrolment and shown once. Only a keyed HMAC
of each is stored, on the same reasoning as invite codes: their entropy is
something a person can write down rather than 256 random bits, so a bare hash
would be attackable offline from a database dump.

A sign-in that has passed the password step gets a challenge, which is a
separate short-lived cookie and is not a session in any sense — nothing in the
application accepts it as proof of who somebody is. Two budgets apply to it:
six attempts against the individual challenge, so one password entry cannot
become a run of guesses, and twenty per account per fifteen minutes, so
somebody who has the password cannot start a fresh challenge for every guess.
A wrong code and a wrong recovery code answer identically.

**Password reset does not bypass the second factor.** Somebody who has taken
over a mailbox has proved one thing, and the whole point of the second factor
is that one thing is not enough.

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

The counters live in SQLite rather than in the process. That used to be the
other way round, and what it cost was worth writing down: a restart handed
whoever was guessing a completely fresh budget, which turns shipping a fix
into an accidental favour to an attacker, and a second instance behind a load
balancer would have kept its own tally and silently halved every limit. The
counter is now as durable as everything else, at the cost of one small upsert
on paths that are already writing to the database.

The limiter fails *open*, and that is a choice rather than an oversight. A
rate limiter is a defence against abuse, not the thing standing between a
stranger and your posts; if the counter cannot be written, refusing to let
anybody post would turn a database hiccup into an outage.

## What Amici sends by email

Four messages, and this is the whole list: confirm your address, confirm an
address you are moving to, reset your password, and your password has changed.
There is no digest, no "people you may know", no "Rosa posted something", and
no re-engagement mail. A social network that emails you to bring you back is
optimising for its own engagement numbers, and Amici does not have any. Every
message says so in its footer.

All four are plain text. No HTML part, no remote images, no tracking pixel, no
click-wrapped links. A social network's email is normally the most heavily
instrumented thing it sends you — it is how the large platforms learn when you
read something and on which device — and none of these tells us that you
opened it.

Nothing in any of them mentions who your friends are or what anybody posted.
An email sits in an inbox that may be read on a shared computer, backed up by
somebody else's provider, and scanned by whatever the provider scans with. The
safe assumption is that anything we put in one is public, so the only thing we
put in one is a link.

The "your password was changed" message deliberately contains no link. It is
the one message nobody asks for, and an unexpected "your password changed,
click here" is indistinguishable from the phishing mail it would teach people
to trust.

Addresses and subjects are rejected if they contain a newline, since header
injection through a display name is one of the older ways to turn a
transactional email into somebody else's spam relay. Delivery refuses to
continue over a connection with no TLS unless a deployment explicitly permits
it for a relay on a trusted interface: the messages here are password reset
links, so one read in transit is an account taken over.

`config.Load` will not start in production without a mail server configured.
Outside production, messages are recorded in an in-memory outbox instead, and
the sender of last resort returns an error rather than swallowing the message
— a confirmation that vanishes silently leaves a member waiting for an email
nobody will ever send.

## Closing an account

Closing takes effect at once: sessions are dropped, the account cannot be
signed in to, outstanding invite codes stop working, and the member's posts
leave their friends' feeds. What it does not do is delete anything for thirty
days.

That delay is not a retention policy dressed up as a kindness. People close
accounts in a bad moment — after an argument, at three in the morning, on a
day that turns out to matter — and a network built for families should not
treat a moment like that as final. Signing in during the window brings
everything back, with no form to fill in and nobody to ask.

After it, the rows are gone: posts, comments, reactions, friendships, invites,
the profile canvas, the email address. Not flagged, not anonymised, deleted,
by the same foreign-key cascades that make the deletion a single statement.
The sweeper that does this runs on the same housekeeping timer as the expired
sessions; if it never ran, closing an account would only ever be hiding it,
and the difference between those two is the whole promise.

Closing asks for the password, because an open session on a shared computer
should not be enough to do the most destructive thing a member can do to their
own account. Support and developer accounts cannot be closed this way at all:
they hold power over other people's accounts, and losing one from a settings
page would leave whoever needs that power without it and no record of why.

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

`/fixtures/outbox` sits behind the same two locks, and needs them more than
the reset endpoint does: it hands out password reset links to anybody who
asks, so it existing anywhere real would be a complete authentication bypass.
It is there because there is no mail server in front of a browser test, and
stubbing the flow out at the service boundary would mean the thing being
tested was not the thing that runs. The suite reads it the way a person reads
an inbox: find the message, follow the link in it.

## Known gaps

Written down rather than left to be discovered. `docs/roadmap.md` says which
of these are queued and roughly in what order.

- **No at-rest encryption.** The SQLite file is plain. Encrypt the volume.
- **Client-address rate limiting is a blunt instrument** where carrier NAT is
  involved. The per-account limits are the ones doing the real work; the
  per-client ones are ceilings on a single machine, pitched well above what
  any household sends.
- **Existing accounts were not backfilled as confirmed** by the migration that
  introduced confirmation, and that is deliberate: marking every existing
  address as proved would paper over exactly the hole the column exists to
  close. The consequence is that anybody who registered before it landed is
  unreachable by email address until they confirm. Their invite codes still
  work, so nobody is cut off in the meantime.
- **The second factor has no QR code.** Drawing one would mean either shipping
  an encoder or asking another website to draw it, and the second would hand a
  third party a shared secret. Typing a secret once is a fair price for now; a
  server-side encoder is queued in [docs/roadmap.md](roadmap.md).
- **There is no way for a member to see or revoke individual sessions.** The
  only control is "sign out everywhere", which is blunt but at least
  honest. A device list would be more useful and is queued in
  [docs/roadmap.md](roadmap.md).
- **Nothing tells a member that somebody signed in from somewhere new.** The
  "your password was changed" notice is the only security alert Amici sends.
- **Suspension and closure have no appeal route** beyond emailing support,
  which is not built either.
- **No key rotation.** `AMICI_SECRET_KEY` keys the HMAC over invite codes and
  recovery codes, so changing it invalidates every live code at once. That is
  survivable but abrupt, and there is no mechanism for accepting an old key
  while a new one takes over.
