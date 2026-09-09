# Deployment

How to release Amici and how to run it. The example files this refers to are
in [`deploy/`](../deploy) and are meant to be copied and edited rather than
read as illustrations.

The short version: Amici is one statically linked binary and one SQLite file.
Put it on a small Linux machine behind Caddy, replicate the database
continuously with Litestream, and deploy by stopping the service, replacing the
file and starting it again. That is not a compromise for the sake of
simplicity — the constraint that makes it the right shape is real, and it is
the first thing below.

## The constraint everything follows from

**Amici is one process holding one SQLite file, and there must never be two.**

Two instances pointed at the same file on the same disk will not politely take
turns. SQLite's locking is per-file and Litestream holds a read lock of its
own; two writers means errors under load and, with the wrong filesystem
underneath, a corrupted database. Two instances pointed at *different* files is
worse in a quieter way: half your members would have accounts the other half
cannot see.

So:

- **No horizontal scaling.** This is fine. A network with no search, no
  discovery and no public content does not go viral by construction, and one
  small machine serves a great many households.
- **No rolling deploys, no blue-green.** Both work by having the new version
  running before the old one stops, which is exactly the thing that must not
  happen. Deployment is stop, swap, start, and it costs a second or two of
  downtime.
- **Nothing that autoscales.** Cloud Run, ECS with more than one task, a
  Kubernetes `Deployment` with `replicas: 2`, Heroku with two dynos — all of
  these will eventually run two copies, and most of them have an ephemeral
  filesystem that loses the database on restart as well.
- **Real disk, not a container layer.** The database needs a volume that
  outlives the process.

When one machine genuinely is not enough, the move is a PostgreSQL
implementation of the store interfaces rather than a second instance; that is
in [roadmap.md](roadmap.md), and everything above the store already talks to
interfaces so that the option stays open.

## Cutting a release

Push a tag. That is the whole ceremony.

```bash
git tag -a v0.1.0 -m "First release"
git push origin v0.1.0
```

[`.github/workflows/release.yml`](../.github/workflows/release.yml) then
re-runs `go vet` and the race suite — a tag can be pushed at a commit whose
checks are old or never ran, so the gate is repeated rather than trusted —
cross-compiles static `linux/amd64` and `linux/arm64` binaries for `amici` and
`amiciadmin`, and publishes them to a GitHub Release with a `SHA256SUMS` file.

Three things about that build are deliberate:

**`CGO_ENABLED=0`.** This is what choosing a pure-Go SQLite driver actually
buys. The result is one file with no libc to match, no shared libraries to
install and no build container needed, so it runs on any Linux of the right
architecture. It is the reason the deployment story below is "copy a file".

**`-trimpath` and `-ldflags "-s -w"`.** The first removes local filesystem
paths, so the binary is reproducible and does not carry a CI runner's directory
layout. The second drops the symbol and DWARF tables, which is most of the
difference between 18MB and 12MB and costs nothing a stack trace needs.

**`amiciseed` is not shipped.** It exists to wipe a database and fill it with
test people. `amiciadmin` is shipped, because changing somebody's role is
something an operator does at a terminal on the machine, so it has to be on
the machine.

The version is stamped from the tag and available as `amici -version`, which
also reports the commit and whether the tree was dirty. It is the first line
in the log at startup, so a report of odd behaviour can always be tied to a
specific build without asking anybody.

### Fixtures are not in a release binary

Amici has exactly one build tag, `fixtures`, and it only ever removes code.
Without it, the endpoints that rebuild the fixture world and read back the
development outbox do not exist — not disabled, not gated, absent.

That matters more than it might sound. `/fixtures/reset` wipes the database,
and `/fixtures/outbox` returns live password reset links to unauthenticated
callers. In a real deployment the second one is not a leak of test data, it is
a complete authentication bypass. It was previously guarded by
`AMICI_ENABLE_FIXTURES` and by `config.Load` refusing that flag in production,
which is decent defence in depth but is two runtime checks on configuration —
the category of thing that gets copied wrong between two deployments at
midnight.

There is a real cost, and it is worth stating rather than glossing: the
browser suite runs a binary built *with* the tag, so it is no longer byte-for-
byte the artifact that ships. That is why the difference is kept strictly
subtractive — every rule about who may see what is identical in both builds —
and why both CI and the release workflow assert that `internal/fixtures` is
unreachable from a default build of `cmd/amici`, with the release additionally
grepping the finished binaries for fixture strings. The guarantee is checked
mechanically rather than asserted in a comment.

Building for yourself, the two forms are:

```bash
go build ./cmd/amici                 # what ships
go build -tags fixtures ./cmd/amici  # what development and the e2e suite use
```

A binary built without the tag warns at startup if `AMICI_ENABLE_FIXTURES` is
set, because a setting that silently does nothing is how somebody loses an
afternoon.

## Running it

A single small VM is the recommended shape: a €4-a-month Hetzner CX22 or the
equivalent is comfortably enough. What follows assumes Debian or Ubuntu.

### Once, at setup

```bash
# A user that owns nothing else.
sudo useradd --system --home /var/lib/amici --shell /usr/sbin/nologin amici
sudo install -d -o amici -g amici -m 0750 /var/lib/amici

# The binaries.
sudo install -m 0755 amici-linux-amd64 /usr/local/bin/amici
sudo install -m 0755 amiciadmin-linux-amd64 /usr/local/bin/amiciadmin

# Configuration, readable only by root.
sudo install -d -m 0700 /etc/amici
sudo install -m 0600 deploy/amici.env.example /etc/amici/amici.env
sudoedit /etc/amici/amici.env

# The service.
sudo install -m 0644 deploy/amici.service /etc/systemd/system/amici.service
sudo systemctl daemon-reload
sudo systemctl enable --now amici
```

Migrations apply themselves at startup and there are no down migrations, on
the reasoning that rolling a schema backwards over live member data deserves a
human writing a forward migration. So the first start creates the schema and
every later start is a no-op. There is nothing to run by hand.

The `amici.service` unit is worth reading rather than pasting. It runs as a
system user with `ProtectSystem=strict`, a syscall filter and one writable
path, which is a lot of hardening for very little configuration, and it uses
`SIGTERM` so that Amici's own graceful shutdown drains in-flight requests
instead of dropping them.

### Deploying a new version

```bash
sudo systemctl stop amici
sudo install -m 0755 amici-linux-amd64 /usr/local/bin/amici
sudo systemctl start amici
sudo systemctl status amici
```

Stop before swap, not after. See the constraint at the top.

### The two settings most likely to be got wrong

**`AMICI_TRUST_PROXY=true` when anything sits in front.** It is off by
default, because believing `X-Forwarded-For` from a client that can set it
freely is worse than not having it. But behind Caddy, leaving it off means
every request appears to come from `127.0.0.1`: every per-client rate limit —
failed sign-ins, registrations, password reset requests — collapses into one
shared bucket for the entire internet, and one determined stranger spends
everybody's budget. This is a quiet failure, because nothing breaks until
somebody is locked out for no reason.

**`AMICI_BASE_URL` must be the real, public, `https://` URL.** It is what goes
into confirmation and password reset links. Wrong, and the mail arrives
containing a link nobody can use.

## Mail

Production will not start without a mail server, which is deliberate: without
one, nobody can confirm an address or recover a password, an account is
unreachable in both directions, and nothing says so. Refusing to boot is the
honest failure.

Use a transactional provider — Postmark is the best of them for exactly this
kind of mail; SES is the cheapest. **Set up SPF, DKIM and DMARC for the
sending domain before inviting anybody.** All four messages Amici sends are
things somebody is actively waiting for, so a password reset in a spam folder
is a locked-out member, and a new domain sending its first authentication mail
without those records is the classic way to land there.

`AMICI_SMTP_ALLOW_PLAINTEXT` cannot be enabled in production. Terminate TLS in
front of a local relay instead if that is the arrangement.

## Backups

**This is the most important item on the page.** Amici is one file on one
machine, so without backups the failure of that one disk is the end of every
message, photograph and friendship anybody put here — and the people affected
chose Amici partly because it promised to look after exactly that.

Use [Litestream](https://litestream.io). It ships the SQLite WAL to object
storage as it is written, so the recovery point is seconds rather than however
long ago the last snapshot ran, and it costs almost nothing at this size.
[`deploy/litestream.yml`](../deploy/litestream.yml) is a working starting
point. Two notes from it worth repeating: give it a credential scoped to
write-only on one bucket, because a backup credential that can also delete is
a backup ransomware takes with it; and Litestream holds a read lock on the
database, which is one more reason there is only ever one instance.

Then **restore one**. An untested backup is a belief, not a backup:

```bash
litestream restore -o /tmp/verify.db s3://amici-backups/amici.db
sqlite3 /tmp/verify.db "pragma integrity_check; select count(*) from accounts;"
```

Do it on a schedule, and do it before you need it.

## Encryption at rest

The database file is plain, and Amici does not encrypt it. Encrypt the volume:
LUKS on a machine you control, or the provider's volume encryption. The
practical threat is not somebody reading the file on a running host, it is a
decommissioned disk or a snapshot copied somewhere it should not have been.

Note what is *already* not in the file, so the volume is the only gap worth
closing: passwords are Argon2id hashes, session tokens and one-shot links are
stored only as hashes, and invite and recovery codes are keyed HMACs whose key
is in the environment rather than the database.

## Before anybody real signs up

- `AMICI_SECRET_KEY` generated once and kept somewhere you will still have in
  two years. There is no rotation mechanism yet, so changing it invalidates
  every outstanding invite and unused recovery code at the same moment, and
  losing it means nobody can ever redeem either again.
- Litestream replicating, and a restore you have actually performed.
- Volume encryption on.
- SPF, DKIM and DMARC for the sending domain.
- The contact address in `/.well-known/security.txt` changed to one somebody
  reads.
- Somebody who will read the reports. Members can report a post, a comment, a
  canvas or an account, and those land in a queue that does nothing at all
  until a person with support access looks at it.
- An `amiciadmin` run to give that person support access.

## What is not solved here

Honest about the gaps this page does not close, all of which are in
[roadmap.md](roadmap.md):

- **`GET /healthz` is a liveness probe, not a readiness one.** It answers
  while the database is unreachable, so it will tell a load balancer that a
  broken instance is fine.
- **There are no metrics.** Structured logs are all there is, so the first
  sign of trouble is a member mentioning it.
- **Housekeeping is a ticker inside the web process**, running at startup and
  hourly. It sweeps expired sessions, spent links, dead rate limit counters and
  accounts past their thirty-day closure grace period. That last one is a
  promise rather than tidiness: if the process is never up for an hour,
  accounts that should have been deleted are not.
- **A single machine is a single point of failure**, and the honest recovery
  story is "restore from Litestream onto a new one", which is minutes of
  downtime and needs a human.
