package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kurtisrogers/amici/internal/brand"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/mail"
)

// notifier composes and sends the four messages Amici will ever send you.
//
// That list is the whole point of this file, so it is worth writing down:
// confirm your address, confirm an address you are moving to, reset your
// password, and your password has changed. There is no digest, no "people you
// may know", no "Rosa posted something", and no re-engagement mail. A social
// network that emails you to bring you back is optimising for its own
// engagement numbers, and Amici does not have any.
//
// Nothing here mentions who your friends are or what anybody posted. An email
// sits in an inbox that may be read on a shared computer, backed up by
// somebody else's provider, and scanned by whatever the provider scans with.
// The safe assumption is that anything we put in one is public, so the only
// thing we put in one is a link.
type notifier struct {
	deps Deps
}

// A confirmation link is a bare URL rather than a code to type, because the
// person receiving it is at their inbox and clicking is what they will do.
// The reset link is the same shape, and both carry the secret in the query
// string, which is why every page reached this way sends no referrer and the
// token is consumed on use.
const (
	confirmPath = "/confirm-email"
	resetPath   = "/reset-password"
)

// sendEmailConfirmation asks somebody to prove they can read the address they
// registered with.
func (n *notifier) sendEmailConfirmation(ctx context.Context, acct *domain.Account, to, token string) error {
	link := n.link(confirmPath, token)
	return n.send(ctx, mail.Message{
		To:      to,
		Subject: "Confirm your email address for " + brand.Name,
		Body: n.body(acct,
			"Somebody, we hope you, signed up to "+brand.Name+" with this address.",
			"",
			"Confirming it does two things. It lets you get back in if you ever forget your password, and it means a friend who types this address into "+brand.Name+" reaches you rather than whoever typed it in first. Until you confirm, nobody can reach you by email address at all, though your request codes still work.",
			"",
			link,
			"",
			"The link works for the next two days. If you did not sign up, you can ignore this and nothing will happen: the account cannot be reached by email until somebody confirms it, and it is not findable by searching, because nothing on "+brand.Name+" is.",
		),
	})
}

// sendEmailChangeConfirmation confirms an address somebody is moving to.
func (n *notifier) sendEmailChangeConfirmation(ctx context.Context, acct *domain.Account, to, token string) error {
	link := n.link(confirmPath, token)
	return n.send(ctx, mail.Message{
		To:      to,
		Subject: "Confirm your new email address for " + brand.Name,
		Body: n.body(acct,
			"You asked to move your "+brand.Name+" account to this address.",
			"",
			link,
			"",
			"Your account keeps working on your old address until you follow that link, so if you have mistyped something you have not locked yourself out. The link works for the next day.",
			"",
			"If this was not you, ignore this message. Nothing changes unless the link is followed, and whoever asked cannot see this address or read anything on the account.",
		),
	})
}

// sendPasswordReset sends a link that lets somebody set a new password.
func (n *notifier) sendPasswordReset(ctx context.Context, acct *domain.Account, to, token string) error {
	link := n.link(resetPath, token)
	return n.send(ctx, mail.Message{
		To:      to,
		Subject: "Set a new password for " + brand.Name,
		Body: n.body(acct,
			"Somebody asked to set a new password for the "+brand.Name+" account with this address.",
			"",
			link,
			"",
			"The link works for the next hour and only once. Setting a new password signs out every browser and phone that was signed in, including whoever asked for this, so if it was not you, they get nothing.",
			"",
			"If it was not you, you do not need to do anything. Somebody knowing your email address is not enough to get in, which is why we sent this here rather than letting them straight through.",
		),
	})
}

// sendPasswordChanged tells somebody their password changed.
//
// This is the one message nobody asks for, and it is here because it is the
// only way a member finds out that somebody else changed their password. It
// deliberately does not contain a link: an unexpected "your password changed,
// click here" is indistinguishable from the phishing mail it would teach
// people to trust.
func (n *notifier) sendPasswordChanged(ctx context.Context, acct *domain.Account) error {
	if !acct.EmailConfirmed() {
		return nil
	}
	return n.send(ctx, mail.Message{
		To:      acct.Email,
		Subject: "Your " + brand.Name + " password was changed",
		Body: n.body(acct,
			"The password on your "+brand.Name+" account was changed just now, and every browser and phone that was signed in has been signed out.",
			"",
			"If that was you, there is nothing to do.",
			"",
			"If it was not, go to "+n.deps.BaseURL+" and use the forgotten password link to take the account back. We have not put a link in this message on purpose: an email telling you to click something urgently is exactly what somebody pretending to be us would send.",
		),
	})
}

// link builds an absolute URL carrying a token.
func (n *notifier) link(path, token string) string {
	return n.deps.BaseURL + path + "?token=" + token
}

// body assembles a plain-text message with Amici's greeting and sign-off.
func (n *notifier) body(acct *domain.Account, lines ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hello %s,\n\n", acct.DisplayName)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n\n—\n")
	b.WriteString(brand.Name + " · " + brand.Tagline + "\n")
	b.WriteString("This is the only kind of message we send. We do not email you about\n")
	b.WriteString("what your friends are doing, and we never will.\n")
	return b.String()
}

// send delivers a message, refusing anything that could carry a header
// injection and logging the failure without logging the address.
func (n *notifier) send(ctx context.Context, m mail.Message) error {
	if err := mail.ValidateAddress(m.To); err != nil {
		return fmt.Errorf("%w: that address cannot be emailed", domain.ErrValidation)
	}
	if err := n.deps.Mailer.Send(ctx, m); err != nil {
		n.deps.Logger.Error("could not send a message",
			slog.String("subject", m.Subject),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("send mail: %w", err)
	}
	return nil
}
