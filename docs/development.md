# Development

## Requirements

Go 1.22 or newer. That is all for the application — the SQLite driver is pure
Go, so there is no C toolchain and no CGO. Node 20 is needed only for the
browser suite in `e2e/`.

## Running it

```bash
go run ./cmd/amiciseed     # build a populated database
go run ./cmd/amici         # start the server
```

Then open http://127.0.0.1:8080 and sign in as `rosa@example.test` with the
password `friends-and-family`.

`amiciseed` prints the whole cast, what each account is for, and any live
friend request codes, so there is no need to read the fixtures source to know
who to sign in as.

## The fixture world

One definition, in `internal/fixtures`, used by both the seed command and the
`POST /fixtures/reset` endpoint that the browser suite calls. That is
deliberate: if the tests had their own fixtures, a test could pass against a
world no developer ever sees, which is how fixtures rot.

Every account uses the password `friends-and-family`, which is long enough to
satisfy the real password policy — fixtures that bypass validation are
fixtures that hide validation bugs.

| Handle | Role | What they are for |
| --- | --- | --- |
| `rosa` | member | The main member. Friends with Teo, Nina and Sofia. Has a profile canvas |
| `teo` | member | Rosa's brother and friend. Use to check a friend can see her posts |
| `nina` | member | Rosa's friend, with a request waiting from Bruno |
| `bruno` | member | A stranger to Rosa in both directions. Use to check a non-friend sees nothing |
| `sofia` | member | Fifteen. Cannot be reached by email at all; her codes expire in four hours |
| `quiet` | member | An adult who switched off email requests. A request to them must silently go nowhere |
| `nuovo` | member | Registered and never confirmed her address. Sees the banner, cannot be reached at `pia@example.test`, cannot reset her password |
| `help-desk` | support | Can look accounts up and suspend them, and cannot read a single post |
| `dev` | developer | Sees diagnostics and the audit trail, and no member content |

Every other fixture address is marked confirmed, which is the only honest
option: their addresses do not exist, so nobody can follow a link to prove
they can read one, and leaving them unconfirmed would produce a development
world in which no member can be reached by the route most of Amici's rules are
about. Pia is the exception because the unconfirmed path needs somebody to
happen to.

The world is shaped around the rules worth exercising: a friendship, a pending
request in each direction, a block, a young member, an opted-out member, an
`only_me` post that must never appear in anybody else's feed, an expired
invite code, an open report, and a profile canvas containing markup that has
to be sanitised.

Bruno being a stranger to Rosa *in both directions* matters. Two people who
each request the other are treated as a mutual acceptance and become friends
immediately, so a pending request from Rosa towards Bruno would make his
request to her succeed instantly and quietly break every stranger scenario.
It did, once.

`amiciseed` is destructive: it empties every table first. It refuses to run
against a production configuration, because a seed script that can be pointed
at production is a seed script that eventually is.

## Configuration

Everything comes from the environment. Defaults are development-friendly.

| Variable | Default | Notes |
| --- | --- | --- |
| `AMICI_ENV` | `development` | `development`, `test` or `production` |
| `AMICI_ADDR` | `127.0.0.1:8080` | |
| `AMICI_DB` | `amici.db` | `:memory:` accepted for tests |
| `AMICI_BASE_URL` | `http://127.0.0.1:8080` | Only used to render invite links |
| `AMICI_SECRET_KEY` | random per start | Keys the invite code HMAC. **Required** in production, minimum 32 characters |
| `AMICI_SECURE_COOKIES` | on in production | Cannot be disabled in production |
| `AMICI_ENABLE_FIXTURES` | on outside production | Cannot be enabled in production |
| `AMICI_TRUST_PROXY` | `false` | Only when something you control is actually in front |
| `AMICI_MAX_REQUEST_BYTES` | `524288` | |

Note what `AMICI_ENV` does *not* change: nothing about sanitising, privacy or
authorisation. It gates whether cookies may travel over plain HTTP and whether
the fixture endpoints exist. A security control you only run in production is a
security control you have never tested.

Outside production, a missing `AMICI_SECRET_KEY` is generated per start rather
than being a startup error. Restarting invalidates local invite codes, which
is fine on a laptop. In production it is a hard failure, because losing the key
makes every outstanding invite code forgeable across restarts.

## Tests

```bash
go test ./...                    # everything
go test -race ./...              # what CI runs
go test ./internal/service/ -v   # the privacy rules
```

The service tests are the real specification of the privacy model, and they
are the ones to read first if you are new to the codebase. They run against a
real SQLite database in a temporary file with a controllable clock, so nothing
sleeps and nothing is mocked.

### Fuzzing the canvas sanitiser

```bash
go test -run '^$' -fuzz FuzzSanitiseNeverEmitsScript ./internal/security/canvas/
```

CI runs this for a minute on every pull request. Run it for longer whenever
you have touched the walker. A fixed set of hostile inputs only proves those
inputs are handled.

### Browser tests

```bash
cd e2e
npm ci
npx playwright install --with-deps chromium
npx playwright test              # both viewports
npx playwright test --headed     # watch it
npx playwright test --ui         # pick through it
npx playwright show-report
```

Playwright builds and starts the server itself, against a throwaway SQLite
file in a temporary directory with fixtures enabled. Nothing needs to be
running first, and the suite never touches a development database.

Two projects: `chromium` (desktop) and `mobile` (Pixel 7 viewport), because a
family social network is mostly read on a phone and the layout has to hold up.
One worker, because the fixture reset endpoint is global state and two workers
would fight over it. The whole suite takes about a minute.

The specs fill forms and click buttons. They do not seed cookies, inject
state, or call into Go. A test that takes a shortcut past the interface stops
being able to tell you the interface works. The single exception is
`POST /fixtures/reset`, and even that goes through CSRF like everything else.

A flake is treated as a bug in the specification, so nothing is retried
locally. CI gets one retry to absorb a slow runner, not to paper over a race.

## Layout

```
cmd/amici          the server
cmd/amiciseed      fixture loader
cmd/amiciadmin     grant and remove support and developer access
internal/domain    entities, validation, ports
internal/service   the rules
internal/store     SQLite implementation of the ports
internal/web       HTTP, templates, static assets
internal/security  passwords, tokens, and the canvas sanitiser
internal/brand     name, voice, colourways
internal/config    environment configuration
internal/fixtures  the fixture world
e2e                Playwright suite
docs               this
```

`docs/architecture.md` explains why the layers are arranged this way and which
direction dependencies point.

## House style

Nothing unusual, but two things this codebase does on purpose.

**Comments explain why, not what.** There are a lot of comments here, and
almost none of them describe what the next line does. They record the reason a
decision went the way it did, especially where the obvious choice was rejected
— the `SameSite=Lax` trade, why sign-in counts failures and not successes, why
there is no `url()` in canvas CSS. Whoever maintains this next needs the
reasoning more than the narration.

**Rules live in the service layer.** If a handler is making a decision or a
query is enforcing a permission, it is in the wrong place. "Only your friends
can see your posts" should be true because of one function that says so, and
testable without an HTTP server.
