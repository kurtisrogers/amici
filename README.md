# Amici

*A little corner of the internet for the people you love.*

**No adverts. No algorithm. No strangers. No selling you.**

Amici is a social network for your friends and family, and nobody else. It is
not trying to compete with the big platforms; it is trying to be the thing
they stopped being. A place away from the noise, where the only thing in your
feed is the people you chose, in the order they wrote it.

## What makes it different

**You can only see your friends' posts.** Visibility has two values, `friends`
and `only_me`. There is no public post, and the type that describes visibility
cannot represent one.

**You cannot find anybody.** There is no search, no discovery, no "people you
may know", no follow, no suggestions of any kind. Read the routing table and
the absence is visible in one screen. This is the product, not a gap.

**Two ways to reach somebody, and no third.** An email address you already
knew, or a short-lived code they handed you — 24 hours, or 4 for a member
under eighteen. Single use. Never stored, only an HMAC of it.

**A request by email tells you nothing.** Whether that address belongs to
nobody, to somebody who has switched email requests off, to a young member who
cannot be reached that way, to somebody who blocked you, or to a friend you
already have: the same sentence, every time. Being able to test whether
somebody is here is exactly what Amici is built to make impossible.

**Search engines get nothing.** `X-Robots-Tag` on every response, a
`robots.txt` that names the AI training crawlers individually, and — the part
that actually matters — a session and a friendship required for every page
worth crawling.

**Companies do not exist here.** No pages, no brands, no advertisers. There is
no role for one and there never will be.

**Children are protected by default.** Nobody under thirteen. Nobody under
eighteen is reachable by email address at all, whatever their settings say,
because knowing a child's email address does not entitle you to reach them.

**You can write the HTML of your own page.** `<marquee>` works. So does
`<blink>`, `<center>`, `<font color="hotpink">`, table layouts and as many
gradients as you like. Some MySpace back, done safely: five independent layers
of defence, documented in [docs/profile-canvas.md](docs/profile-canvas.md).

**Nothing is loaded from anybody else.** No CDN, no font service, no
analytics, no error reporting, and no external images in profiles — every
external URL in a profile is a request from the *viewer's* browser to a
stranger's server, carrying their address and the time they looked.

**No photographs of faces.** An avatar is initials on a colour. A photo of a
child's face is the single most sensitive thing a social network can hold, and
the safest way to hold it is not to.

## Try it

Needs Go 1.22 or newer. Nothing else: the SQLite driver is pure Go, so there
is no C toolchain, no CGO and no build step.

```bash
go run ./cmd/amiciseed     # build a populated database
go run ./cmd/amici         # start the server
```

Open http://127.0.0.1:8080 and sign in as `rosa@example.test` with the
password `friends-and-family`. The seed command prints the whole cast and what
each account is there to demonstrate — a stranger, a friend, a fifteen year
old, somebody who has switched off email requests, a support account and a
developer account.

Worth a look once you are in: Rosa's profile page, which is deliberately built
from markup that has to be sanitised; the support console signed in as
`support@example.test`, to see how little of a member's life it can reach; and
the colourway picker in settings, which re-skins the whole place.

## How it is built

Server-rendered Go, SQLite, PicoCSS, and no JavaScript at all. A browser spec
asserts that the whole application — posting, reacting, commenting — works
with scripting switched off.

```
internal/domain    entities, validation, and the repository interfaces
internal/service   the rules: who may see what
internal/store     SQLite implementation of those interfaces
internal/web       HTTP, templates, static assets
internal/security  passwords, tokens, and the profile canvas sanitiser
```

Dependencies point one way, downwards. "Only your friends can see your posts"
is true because of one function in the service layer that says so, testable
without an HTTP server. Swapping SQLite for PostgreSQL means implementing the
interfaces and changing one line in `cmd/amici`.

Four direct dependencies: `bluemonday`, `golang.org/x/crypto`,
`golang.org/x/net`, `modernc.org/sqlite`. No web framework, no router, no ORM.

## Tests

```bash
go test -race ./...              # the Go suite
cd e2e && npx playwright test    # 84 browser specs, two viewports
```

Three layers, each covering what the others cannot. The **service tests** are
the specification of the privacy model, and the best place to start reading.
The **store tests** cover the SQL, including the feed query, where a mistake
shows somebody the wrong post. The **browser specs** drive a real server in a
real browser — filling forms, clicking buttons — to check the things that can
only be true in a browser: that sanitised markup really is inert, that the
frame sandbox really is applied, that a page a stranger cannot see really
answers 404.

Plus a fuzz target on the canvas sanitiser, which runs for a minute on every
pull request. Both suites run on every pull request via GitHub Actions.

## Documentation

| | |
| --- | --- |
| [docs/architecture.md](docs/architecture.md) | The layers, why they are arranged this way, and what is left undone |
| [docs/security.md](docs/security.md) | The privacy model, authentication, CSRF, headers, and the known gaps |
| [docs/profile-canvas.md](docs/profile-canvas.md) | The five layers that make member-authored HTML safe. Read before touching the sanitiser |
| [docs/accounts.md](docs/accounts.md) | Roles, capabilities, and what support genuinely cannot see |
| [docs/brand.md](docs/brand.md) | The name, the voice, the six colourways |
| [docs/frontend.md](docs/frontend.md) | Templates, PicoCSS, and why there is no JavaScript |
| [docs/development.md](docs/development.md) | Running it, the fixture cast, configuration, tests |
| [docs/roadmap.md](docs/roadmap.md) | What is planned, and the longer list of what Amici refuses to build |

## Before running this for real

Amici is a foundation, and honest about what is missing. The gaps are listed
in full at the end of [docs/security.md](docs/security.md) and what is queued
against them is in [docs/roadmap.md](docs/roadmap.md). These are the ones to
deal with before anybody real signs up.

- **Set `AMICI_SECRET_KEY`.** Production refuses to start without it. It keys
  the HMAC over invite and recovery codes, and there is no rotation mechanism
  yet, so changing it later invalidates every live code at once.
- **Configure a mail server.** Production refuses to start without one, because
  without it nobody can confirm an address or reset a password.
- **Encrypt the volume.** The SQLite file is plain.
- **Arrange backups, and restore one somewhere safe** to confirm the backup is
  a backup rather than a belief.
- **Change the address in `/.well-known/security.txt`.**
- **Run one instance.** The store is a local SQLite file, so a second instance
  is not a scaling step, it is two different services.
- **Somebody has to read the reports.** Members can report a post, a comment,
  a canvas or an account, and those land in a queue that does nothing until a
  person with support access looks at it.

## Licence

Not yet chosen.
