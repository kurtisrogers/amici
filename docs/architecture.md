# Architecture

Amici is a server-rendered Go application with a SQLite database and no
JavaScript. That is three deliberate choices, so they are worth stating before
the layout.

**Server-rendered.** Every page Amici serves is complete HTML. There is no
client-side framework, no hydration and no API for a front end to call. A
social network for families should work on a cheap phone on a bad connection,
and it should keep working when a member has script switched off. One of the
browser specs asserts exactly that: the whole application, including posting
and reacting, works with JavaScript disabled.

**SQLite.** A network built so that people cannot be found does not need a
distributed database. One file is easier to back up, easier to reason about,
and easier to hand to whoever maintains this next. The driver is
`modernc.org/sqlite`, which is pure Go, so building Amici needs no C toolchain
and cross-compiling is unremarkable. Nothing in the service layer knows it is
talking to SQLite; see *Ports* below.

**No JavaScript.** Not a purity exercise. Amici accepts HTML from members and
shows it to their friends, and the smaller the amount of script on the origin,
the less there is for an escaped canvas to reach. It also means a Content
Security Policy of `script-src 'self'` costs nothing, because there is nothing
to load.

## Layers

Dependencies point one way only, downwards.

```
cmd/amici, cmd/amiciseed, cmd/amiciadmin    entrypoints
        │
internal/web                                HTTP: routing, sessions, CSRF, templates
        │
internal/service                            the rules: who may see what
        │
internal/domain                             entities, validation, ports
        │
internal/store/sqlite                       one implementation of the ports
```

Alongside those: `internal/security` (password hashing, tokens, the second
factor) and `internal/security/canvas` (the profile sanitiser), both leaf
packages; `internal/mail`, which is a `Sender` interface and three
implementations and knows nothing about what a message says; `internal/brand`
(name, voice, colourways); `internal/config`; and `internal/fixtures`.

`internal/mail` is a port in the same sense as the store, and for the same
reason. The service layer composes the four messages Amici sends and hands
them to a `Sender`; whether that is an SMTP server, an in-memory outbox the
browser tests read, or a sender that loudly refuses, is a wiring decision made
once in `cmd/amici`.

### internal/domain

Entities, value types and validation. No database, no HTTP, no dependencies
outside the standard library. If you want to know what a friend request *is*,
this is the only file you need.

Two things here are worth pointing out. Identifiers are random 128-bit values
rendered in base32, not sequential integers, because a sequential identifier
in a URL tells you how many members there are and lets you walk the set.
And `Visibility` has exactly two values, `friends` and `only_me`. There is no
`public`. The type system refuses to represent a public post.

### internal/domain/ports.go

The interfaces the service layer talks through: `AccountRepo`, `SessionRepo`,
`FriendRepo`, `InviteRepo`, `PostRepo`, `CanvasRepo`, `AuditRepo`,
`ReportRepo`, gathered into a `Store`. Also `Clock`, which is what makes every
time-dependent rule testable — invite expiry, session expiry, rate limit
windows — without a test ever sleeping.

Ports are why "swap SQLite for PostgreSQL" is a contained job rather than a
rewrite: implement the interfaces, change one line in `cmd/amici`. The service
layer would not need to change at all.

### internal/service

Where the rules live. "Only your friends can see your posts" is true because
of code in this package, not because of a template that happens not to render
something or a query that happens to filter.

Every service method takes the acting account and checks a capability before
doing anything. Errors are the sentinels in `domain/errors.go`
(`ErrValidation`, `ErrNotFound`, `ErrForbidden`, `ErrRateLimited`, and so on),
which the web layer maps to status codes and messages. The service layer never
formats HTML and never knows a request exists.

The service tests are the real specification of the privacy model. They run
against a real SQLite database in a temporary file, with a controllable clock,
and they are fast enough to run on every save.

### internal/store/sqlite

Hand-written SQL. No ORM and no query builder: the queries that matter are the
ones that decide what a member can see, and those should be readable as SQL by
somebody auditing them.

Migrations are numbered files in `migrations/`, embedded and applied at
startup.

### internal/web

Routing, middleware, sessions, CSRF, templates. Handlers parse forms, call one
service method, and render. When a handler starts making decisions, the
decision belongs in the service layer.

Routing uses Go 1.22's `http.ServeMux`, which handles method-and-pattern
matching, so there is no router dependency. `routes.go` is grouped by who can
reach what, which means the whole authorisation surface is readable on one
screen — and so is the absence of `/search`, `/discover` and `/suggestions`.

The middleware stack, outermost first: panic recovery, request context and
body limit, logging, security headers, session authentication, CSRF. Then
per-route, either nothing, `requireViewer`, or `requireCapability`.

Templates are `html/template`, embedded with `go:embed`, parsed once at
startup. Contextual auto-escaping is the reason a member's display name cannot
become script. The one place member markup is rendered as markup is the canvas
frame, and that goes through the sanitiser and lands in a sandboxed iframe.

## Request lifecycle

A `POST /posts` from a signed-in member:

1. `recoverPanics` arms a deferred recover.
2. `requestContext` attaches a request id and wraps the body in a
   `MaxBytesReader`.
3. `secureHeaders` sets the CSP, `X-Robots-Tag` and the rest.
4. `authenticate` reads the session cookie, resolves it to an account, and
   attaches it to the context. It never rejects; routes decide.
5. `csrf` verifies the origin and the double-submit token.
6. `requireViewer` returns 401 without a session.
7. The handler parses the form and calls `Feed.CreatePost`.
8. The service checks the capability and the rate limit, validates the body,
   writes through `PostRepo`, and returns.
9. The handler sets a flash and redirects. Post-redirect-get, so a refresh
   does not post twice.

## Testing

Three layers, each testing something the others cannot.

**Service tests** (`internal/service/*_test.go`) are the privacy
specification: a non-friend sees nothing, an `only_me` post is invisible to
friends, support cannot read a post, a request by email answers identically
whoever it was sent to. Real database, fake clock.

**Store tests** (`internal/store/sqlite`) cover the SQL, including the feed
query, which is the one query where a mistake shows somebody the wrong post.

**Browser tests** (`e2e/`) drive a real server in a real browser: fill the
form, click the button, read the page. They cover what the other two cannot —
that the sanitiser's output really is inert in a browser, that the sandbox
attribute is really there, that a page a stranger cannot see really answers
404. Two viewports, desktop and phone.

Plus a fuzz target on the canvas sanitiser, which runs for a minute on every
pull request. A fixed set of hostile inputs only proves those inputs are
handled.

## Things left undone

Recorded honestly rather than discovered later. What is queued, and roughly in
what order, is in [docs/roadmap.md](roadmap.md); the security-specific gaps are
listed at the end of [docs/security.md](security.md).

- **A friend request is not announced by email.** It appears on the
  recipient's friends page next time they look. Now that there is a mailer
  this is a choice rather than a limitation, and it is the one place where the
  "we only send four messages" rule costs a member something real: a request
  can sit unseen for as long as somebody goes without signing in.
- **Nothing runs in more than one process.** The rate limit counters moved to
  the database, so the arithmetic would survive a second instance, but SQLite
  with a local file would not. Running two of these means moving the store
  first, which is what the ports are for.
- **No pagination on the friends page or the support console.** The feed is
  paginated; those two are not, on the assumption that a person has friends
  rather than followers. The support console is the one that will break first,
  because its list grows with the whole service rather than with one household.
- **Housekeeping is a timer in the process** rather than a job with its own
  lifecycle. It sweeps expired sessions, spent tokens, dead rate limit
  counters and accounts past their grace period. If the process is never up
  long enough to fire it, none of that happens, and the closure sweep is the
  one where "did not run" is a broken promise rather than untidiness.
