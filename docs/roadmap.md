# Roadmap

What Amici intends to build, what it has decided not to build, and why. It is
a themed roadmap rather than a schedule: items sit in **Now**, **Next** or
**Later** according to what they depend on and how much they matter, and none
of them carries a date. A date on a document like this is a promise made by
somebody who does not yet know what the work involves.

Two things make this document worth keeping rather than a wish list nobody
reads. The first is that every item traces back to a gap recorded somewhere
concrete — the end of [security.md](security.md), *Things left undone* in
[architecture.md](architecture.md), the accessibility section of
[frontend.md](frontend.md) — so the roadmap and the honest self-assessment
cannot drift apart. The second is the [Not on the roadmap](#not-on-the-roadmap)
section, which is the more important half. A social network is defined by what
it refuses to build, and the refusals need to be written down in the same place
as the ambitions or they will be quietly relitigated every time someone new
arrives with a good idea.

## How to read it

| Horizon | Means |
| --- | --- |
| **Now** | Accepted, understood, and next to be picked up. Nothing here is blocked on anything unbuilt. |
| **Next** | Accepted in principle. Either it depends on something in Now, or the design question at the bottom of it is not settled. |
| **Later** | Wanted, not urgent, and likely to change shape before it is built. |
| **Exploring** | An open question rather than a commitment. Listed so the question is visible, not so it looks planned. |

Items are grouped by theme, and each theme carries both halves of the work.
Splitting a roadmap into a user-facing list and a backend list is how backend
work becomes invisible until it is an emergency; here the sessions page and the
key rotation that makes revocation meaningful sit next to each other, because
they are the same job seen from two ends.

---

## Theme: getting in, and getting back in

Account security. This is where most of the recent work went, and the parts
that remain are the ones a member notices only at the worst possible moment.

### Now

**A list of where you are signed in, with individual revoke.** Today the only
control is "sign out everywhere", which is blunt but honest: it is the right
tool when a laptop is stolen and far too large a hammer for "I used a friend's
computer once". Sessions already carry a user agent and a creation time, so the
page is mostly a query and a template. The revoke path is the interesting part,
because a session list that cannot promise a revoked session is *immediately*
dead is worse than no list at all — sessions are checked against the database
on every request, so that promise holds, and the test needs to assert it rather
than assume it.

**A QR code for the second factor.** Enrolment currently shows a base32 secret
to be typed by hand, because the alternatives were shipping an encoder or
asking another website to draw the code, and the second would hand a third
party a shared secret. The first alternative is simply work: a QR encoder for a
fixed-length `otpauth://` URI is a small, well-specified amount of code, and it
must be server-side and inline, since a remote image would leak the secret in a
URL to whoever hosts it. Typing a twenty-character secret is the single largest
piece of friction in enrolling, and enrolment friction is measured in accounts
that do not get a second factor.

**Key rotation for `AMICI_SECRET_KEY`.** The key covers the HMAC over invite
codes and recovery codes, so changing it today invalidates every live code at
once — survivable, abrupt, and enough of a deterrent that in practice the key
would simply never be rotated, which is the real problem. The fix is a
primary key plus a list of retired keys still accepted on read, with a value
rewritten under the current key whenever it is successfully verified under an
old one. Rotation then becomes a deploy rather than an incident.

### Next

**Telling somebody that their account was signed in to from somewhere new.**
This is the first item that costs something to build in principle rather than
in code. Amici sends four messages and its email footer promises that is the
whole list; a sign-in alert is a fifth. The promise is worth more than most
features, so the question is whether a security alert is a different category
of message from the engagement mail the promise exists to rule out. The
tentative answer is that it is — nobody has ever felt manipulated by "your
password was changed", and that message already exists — but the footer, the
policy in [security.md](security.md) and this paragraph all have to be changed
together, deliberately, rather than an alert being added and the promise
quietly becoming untrue.

**A route to challenge a suspension or a closure.** Support can suspend an
account, closure deletes one after thirty days, and neither has an appeal
route beyond emailing an address that is not built. For a service that holds
families' photographs of each other, "we decided, and there is nobody to ask"
is not an acceptable end state.

### Later

**Passkeys.** The right long-term answer to both passwords and TOTP: nothing
to type, nothing to phish, and no phone number anywhere near it. It is Later
rather than Next because WebAuthn brings a real amount of client-side script
into an application that currently has none, and that trade needs thinking
about properly against the canvas threat model rather than waved through
because the feature is good.

---

## Theme: safety of the household

Amici exists to be a place where a family can be online together, and the
protections for the youngest members are the least finished part of it.

Reporting itself is built: a member can report a post, a comment, a profile
canvas or an account, it is rate limited, and it lands in a queue support can
resolve. The reason field is the mechanism that lets support act without being
able to read member content — the reporter quotes what they saw, which puts
the decision to share a friend's words in the hands of the person who received
them. What is unfinished is everything either side of that queue.

### Now

**Closing the loop with the person who reported something.** A report is
resolved and the member who raised it is never told anything. That is the
single most demoralising possible outcome: somebody worried enough to fill in
a form learns only that nothing visibly happened, which teaches them not to
bother next time. It runs into the four-messages promise the same way the
sign-in alert does, and probably wants an in-application answer rather than an
email, since "we looked at what you told us" does not need to sit in an inbox.

**Grouping reports about the same subject.** Five members reporting one
account produces five unrelated rows, so the one signal that most reliably
distinguishes a misunderstanding from a real problem — how many different
people independently reported it — is invisible to the person triaging. The
queue also has no pagination and no ordering beyond recency, which is fine for
a fixture world and not for a real one.

### Next

**Something for a parent or guardian of a young member.** Today an account for
a fifteen-year-old differs only in that it cannot be reached by email and its
invite codes expire in four hours. That is a sensible default and it is not
oversight. What it should become is genuinely open: full visibility for a
parent is surveillance, and teenagers who are surveilled move somewhere else
rather than behave differently, so the useful version is probably closer to
"who can reach my child" than "what my child said".

### Exploring

**Blocking that survives account deletion.** A block today is a row that
disappears when either account does. Somebody who deletes and re-registers
gets a clean slate against the person who blocked them, which is exactly the
person it should not work against. A durable answer means keeping some
identifier of a deleted account, which cuts directly against the deletion
promise in [security.md](security.md). Written down here because the tension
is real and unresolved, not because a solution is planned.

---

## Theme: your own page

The profile canvas is the feature people will talk about, and it is the one
carrying the most risk.

### Next

**A gallery of starting points.** The canvas is a blank textarea containing
HTML, which is thrilling for the two percent of members who grew up doing this
and forbidding for everybody else. A set of themes to copy, fork and mangle is
how the MySpace era actually worked — almost nobody wrote their page from
nothing, they pasted somebody else's and changed the colours.

**Telling somebody what the sanitiser removed, and why.** Today prohibited
markup is stripped silently, so a member who pastes a template containing a
script tag sees their page render slightly wrong and has no idea the sanitiser
is the reason. A diff-style report at save time turns a confusing silence into
an explanation.

### Later

**A cost budget for a canvas, rather than only a size one.** The source is
already capped at 24KB of HTML and 12KB of CSS, which bounds what a member can
store but not what it costs to display: the CSS allowlist permits animations,
and a page well inside the byte limit can still make a cheap phone
uncomfortable. Nobody has measured this, so it is Later and phrased as a
suspicion rather than a finding.

---

## Theme: operating Amici

Nothing in here is visible to a member. All of it is visible to whoever is
awake at three in the morning when something is wrong.

### Now

**Backups, and a restore that somebody has actually performed.** Amici is one
SQLite file, which makes backup unusually easy and therefore unusually easy to
assume is handled. The item is not "write a backup script"; it is "restore
from a backup into a scratch environment and confirm the result is a working
service", because an untested backup is a belief rather than a backup.

**Encryption at rest.** The database file is plain today. Whole-volume
encryption is the pragmatic answer and mostly a deployment concern, but it is
listed here because it is currently listed as a known gap and nobody owns it.

**A readiness endpoint, and a small set of metrics.** `GET /healthz` exists
and, as its own comment says, says nothing about the system — it answers while
the database is unreachable, which makes it a liveness probe and not the thing
you want a load balancer consulting before it sends somebody a page. Beyond
that there is structured logging and nothing else, so the only way to know
Amici is struggling is that somebody says so. The metrics worth having are
boring:
request rate and latency by route, database errors, mail send failures, and
the counts the housekeeping sweep already produces. Deliberately no per-member
anything — an operational metric that can be narrowed to one person is
telemetry wearing a high-visibility jacket.

**Housekeeping as a job rather than a timer.** The sweep that deletes expired
sessions, spent tokens, dead rate limit counters and accounts past their grace
period runs on a ticker inside the web process. If the process is never up
long enough to fire it, none of it happens, and one of those four is a
promise: an account past its grace period is *supposed* to be gone. It needs
to be runnable on demand, observable when it runs, and loud when it does not.

**Pagination on the friends page and the support console.** The feed is
paginated and these two are not, on the reasonable assumption that a person
has friends rather than followers. That assumption holds for the friends page
and does not hold at all for the support console, whose list grows with the
size of the whole service.

### Next

**A PostgreSQL implementation of the ports.** Everything above the store
already talks to interfaces and no service knows it is talking to SQLite, so
this is the test of whether that discipline was worth it. It is not needed for
scale — a network with no discovery does not go viral — but it is needed for
running more than one instance, which is what makes a deploy something other
than a small outage.

**A staging environment that runs the production wiring.** Several classes of
bug can only appear where the mailer is real SMTP rather than an outbox and
the environment is `production` rather than `development`, and those are
precisely the paths nobody exercises until a member does.

### Later

**Load and soak testing.** Not for a scale Amici expects, but to find the
query that is fine with the fixture cast and quadratic with a real one. The
feed query is the obvious candidate and the one where being wrong is worst.

---

## Theme: reach and accessibility

### Now

**An automated accessibility audit in CI.** The browser suite already drives
every page, so adding an axe pass to those runs is cheap and turns
accessibility from an intention into a check that can fail. It catches contrast
and labelling and structure, which is the majority of what actually goes wrong.

**A screen reader pass by a person.** Automated audits cannot tell you that a
page is *usable*, only that it is not obviously broken.
[frontend.md](frontend.md) currently claims intent plus whatever the specs
happen to assert, and that gap should be closed by testing rather than by
softening the claim.

### Next

**Take your things with you.** A member should be able to download everything
Amici holds about them — posts, comments, reactions, friendships, their canvas
— in a form that is readable without Amici. This is a legal requirement in
several places Amici would like to operate, and independently of that, a
service that makes leaving difficult has stopped competing on being good.
Export is also the humane companion to closure: thirty days is a grace period,
not an archive.

### Later

**Translation.** A network built around families is a network built around
whatever language a family speaks at home, and every string is currently
hard-coded English. The name is Latin; the assumption should not be.

---

## Not on the roadmap

Refusals, not omissions. Each of these is a thing a social network is normally
expected to have, and each is absent on purpose. If one of them ever ships,
Amici has become something else, and that should be an explicit decision made
by a person who has read this list.

- **Search, directories, and any other way to find a person you do not
  already know.** This is the founding constraint. Every feature here is
  downstream of it.
- **Public posts.** `Visibility` has two values and neither is `public`. The
  type system refuses to represent one.
- **Follows, followers, and follower counts.** Friendship is mutual and
  unranked. An asymmetric edge is what turns a social network into an audience.
- **Company pages, brand accounts, verified badges.** Amici is for people.
- **Advertising, tracking, third-party analytics, and any external resource
  loaded by a member's browser.** No fonts from a CDN, no pixel, no embeds.
- **An algorithmic feed.** Reverse chronological, and nothing decides on a
  member's behalf what matters.
- **Engagement email.** No digests, no "people you may know", no "come back,
  you have not visited". The list of messages Amici sends is fixed and short
  and printed in the footer of every one of them.
- **Photo uploads for avatars.** Initials only. A service that stores
  photographs of children's faces has taken on a responsibility Amici is not
  willing to take on for a decorative circle.
- **SMS as a second factor.** A phone number is the most re-identifiable thing
  a person can hand over, and SMS is the weakest common second factor, since
  it can be taken from somebody by talking to their mobile operator.
- **A native mobile app.** The web application works on a cheap phone on a bad
  connection, which is the actual requirement. An app store listing is also a
  discovery surface, and the point of Amici is not being discovered.

## Recently shipped

Kept short and pruned as it ages; the full history is in the commit log. This
is here so the roadmap reads as a live document rather than a list of things
that never happen.

- Outbound mail, with a `Sender` port, an SMTP implementation, an in-memory
  outbox for development and browser tests, and a default that fails loudly
  rather than swallowing a message.
- Confirmed email addresses. An unproved address cannot be reached by a friend
  request and cannot receive a password reset, which closes the gap where
  registering with somebody else's address quietly intercepted their requests.
- Password reset by single-use link, with an answer that is identical whether
  or not the address has an account behind it.
- A second factor: TOTP implemented directly against RFC 6238, two-step
  enrolment, and ten single-use recovery codes stored as keyed hashes.
- A password policy beyond length: an embedded breach corpus, structural
  pattern checks, and rejection of passwords built out of the member's own
  handle, name or address.
- Member-facing account closure, with a thirty-day grace period during which
  signing in undoes it, and real deletion afterwards.
- Rate limit counters moved from process memory into the database, so a
  restart no longer hands whoever is guessing a fresh budget.
- Call-to-action links that are announced as links, replacing the
  `role="button"` convention inherited from PicoCSS.

## How this document is maintained

An item earns a place here by being a gap somebody has actually hit or
recorded, not by being a good idea in the abstract. When one is built, it moves
to *Recently shipped*, the gap it came from is deleted from wherever it was
recorded, and the documentation that described the old behaviour is corrected
in the same change. That last part is the rule that keeps the rest true: a
feature is not finished when the tests pass, it is finished when nothing in
`docs/` still describes the world before it.
