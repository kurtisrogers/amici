# Accounts

## Roles

Three, and the set is meant to stay this size.

| Role | Who | Can | Cannot |
| --- | --- | --- | --- |
| `member` | A person keeping up with people they love | Post, comment, react, edit their own profile canvas, send friend requests | Anything about anybody else's account |
| `support` | Helps people back into their accounts and handles reports | Everything a member can, plus: look an account up by email or handle, suspend and restore, act on reports, switch a canvas to plain rendering | Read a post, a comment, or a friend list |
| `developer` | Operates the service | Everything a member can, plus: diagnostics, the audit trail, re-rendering canvases | Read any member content, or change what an account is allowed to do |

There is no "page", "brand", "organisation" or "advertiser" role. Companies do
not get a presence on Amici. That is not a gap to be filled in later; it is
one of the reasons Amici exists.

## Capabilities, not role checks

Services ask "may this actor do this?", never "is this actor an admin?". The
capabilities are in `internal/domain/account.go` and the mapping from role to
capability set is one table.

This matters for maintenance. Adding a role means adding a row to that table.
Checking roles inline instead would mean auditing every call site each time
the set changed, and eventually one of them would be missed.

`requireCapability` in the service layer is the single gate. It also checks the
account is active, so a suspended support account loses its powers without
anybody having to remember to check status separately.

## What support genuinely cannot see

Worth stating plainly, because "admins can see everything" is the norm
elsewhere and the norm is the problem.

The support console has no route to member content. Not "it is not linked" —
there is no capability for it, so there is no service method that would answer,
so there is nothing for a handler to call. Support sees counts (how many
friends, how many posts), status, role, join date, and whether the member is
under 18. Not a word of what anybody wrote.

The one exception is a report, and it is not really an exception: a report is
the *reporter's own description* of something, written by them, on purpose, to
be read by support. Support acting on a report cannot open the post it refers
to. Only the subject's identifier is recorded, so the trail is auditable
without the content being readable.

The console says all of this on the page, to the person using it. A support
tool that quietly could read posts would eventually be used to, and the most
common way a support tool hurts people is by making private things routine to
look at.

## Suspension

Support can suspend an account. That closes every session it has open, refuses
future sign-ins, and removes the member's posts from their friends' feeds — the
feed query filters on author status, so suspension is not just a sign-in
block. Restoring reverses all of it.

A suspended account gets told it is suspended *after* its password verifies,
never before. Answering "this account is suspended" to anybody who types the
address would turn suspension into public information.

Every suspension and restoration carries a reason and goes into the audit
trail against the name of the person who did it.

## Granting a role

Only from a shell, with `cmd/amiciadmin`:

```bash
go run ./cmd/amiciadmin -handle marco -role support
go run ./cmd/amiciadmin -handle marco -role member -reason "left the team"
```

There is deliberately **nowhere in the interface** that hands out power over
other people's accounts. An attacker who takes over a support session cannot
use it to make more support accounts, and a developer console compromise
cannot escalate itself. Changing what an account may do requires access to the
machine holding the database.

Every change is written to the audit trail, where both consoles show it. The
account is recorded as its own actor, with the detail making clear the change
came from a shell rather than from somebody clicking something — pretending an
anonymous operator was a member account would be worse than being plain about
it.

Role changes take effect on next sign-in for console visibility, because the
navigation is built from the account loaded with the session.

## Registration

- Display name, handle, email address, date of birth, password.
- Handles are lowercase letters, numbers, dots, dashes, underscores. A handle
  is the address of a profile page, and only friends can open it.
- Minimum age 13. Under 18, extra protections apply automatically.
- Password minimum 10 characters, no character-class rules.
- A duplicate handle and a duplicate email address produce the *same* message.
  A registration form that distinguishes them is a way to test whether an
  address is registered.
- Joining signs you straight in and lands you on the friends page rather than
  the feed, because a feed with nobody in it is not a useful first screen.

Registration sends a confirmation link, and the address is unproved until
somebody follows it. An unproved address cannot be used to reach the member
and cannot receive a password reset, which is what stops registering with
somebody else's address being a way to receive their friend requests.

Nothing else is withheld. The member is signed in, can post, and can reach
people with request codes; the only thing an unconfirmed address costs is
being reachable by email. So the reminder is a banner rather than a wall, and
somebody who never wants to be reachable that way can ignore it forever.

## Changing the email address

From settings, with the current password. The new address is stored as pending
and the account keeps working on the old one until a link sent to the new one
is followed. That ordering is the whole safety of the flow: a typo costs a
wasted email rather than an account nobody can recover, and somebody who
briefly gets hold of a session cannot take the account away by pointing it at
an address they control.

Whether the new address is already on another account is not reported to
whoever asked, for the same reason registration has one message for every
collision. The pending address is recorded and the confirmation is sent; the
collision surfaces when the link is followed, to the person who by definition
owns that mailbox.

Completing a change invalidates any password reset links sent to the old
address, so the previous owner of that mailbox does not keep a way in.

## A second step when signing in

Optional, off by default, and a code from an authenticator app rather than a
text message. `docs/security.md` explains why there is no SMS option and how
the enrolment and challenge flows are shaped. From a member's point of view:

- Enrolling shows a secret to type into an app, then asks for a code to prove
  the app is working. Nothing changes until that code is right.
- Ten recovery codes are shown once, at enrolment, and never again. Any of
  them gets you in without your phone, once each. A fresh set can be made from
  settings, which invalidates the old one.
- Turning it off needs the password, and takes the recovery codes with it.
- Nobody at Amici can let a member past the second step, and the page says so.
  Support being able to would mean anybody who could talk their way past
  support could too.

## Age

| | Under 18 | 18 and over |
| --- | --- | --- |
| Reachable by email address | Never, whatever the settings say | Yes, if they leave it on |
| Invite code lifetime | 4 hours | 24 hours |
| Flagged in the support console | Yes | No |

Under-18 reachability is enforced in `deliver`, which both request routes funnel
through, so the rule cannot drift apart between them.

There is no separate child account type. A young member gets the same product
with stricter reachability, because a visibly different kind of account is
itself a signal to anybody looking for one.

## Sessions

- 30 days, extended on use.
- One row per session, so a member can see how many browsers are signed in and
  revoke the others from settings.
- Changing a password, resetting one, or closing an account closes every
  session.
- Expired sessions are purged by a housekeeping pass, and any session found
  expired during authentication is deleted there and then.

There is no per-device list yet: the only control is "sign out everywhere",
which is blunt but honest. A device list is queued in
[docs/roadmap.md](roadmap.md).

## Forgotten passwords

A link sent to the confirmed address on the account, good for an hour and for
one use. Setting a new password closes every session, including that of
whoever asked for the reset — so if the reason for the reset was that somebody
else had the password, they get nothing out of it.

The form answers identically every time, whether the address belongs to
nobody, to a suspended account, to a closed one, or to an account that never
confirmed its address, and it says out loud that it is not telling you which.
Somebody who typed a friend's address in to see what would happen learns
nothing, and somebody whose own reset never arrives can at least understand
why they are not being told.

Support cannot do this for a member, and that is not unhelpfulness. If support
could hand an account to whoever asked convincingly enough, so could anybody
who learned to ask the same way.

## Closing your own account

From settings, with the password, and the page counts out what it costs before
it happens: how many posts will leave your friends' feeds, how many friends
you are connected to, and how long you have to change your mind. "Close my
account" is an abstraction; a number is not, and nobody should discover
afterwards what they agreed to.

It takes effect at once — signed out everywhere, invisible to friends, invite
codes dead — and deletes nothing for thirty days. Signing in inside that
window brings everything back with no form to fill in and nobody to ask. After
it, `PurgeClosedAccounts` deletes the rows for good. `docs/security.md`
explains why the grace period is shaped that way.

Support and developer accounts cannot be closed from settings. They hold power
over other people's accounts, so they are moved back to `member` with
`amiciadmin` first, by somebody who has to be at a terminal.

`StatusDeactivated` is what a closed account is, and it is honoured everywhere
`StatusSuspended` is: the feed query filters on author status, so closure is
not merely a sign-in block. The difference between the two is who asked for it
and whether it ends in deletion.
