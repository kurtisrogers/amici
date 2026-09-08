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

Registration does not confirm the email address. That is the most significant
gap in the system and `docs/security.md` explains what it means.

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
- Changing a password closes every other session.
- Expired sessions are purged by a housekeeping pass, and any session found
  expired during authentication is deleted there and then.

## Deactivation

`StatusDeactivated` exists and is honoured everywhere `StatusSuspended` is,
but there is no member-facing route to it yet. Closing your own account should
be as easy as opening one, and that is a known gap rather than a decision.
