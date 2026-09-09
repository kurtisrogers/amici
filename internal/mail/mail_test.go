package mail

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// A Sender is never nil, and the one that stands in for "nothing is
// configured" has to fail rather than swallow. A confirmation link that
// vanishes silently leaves a member waiting for an email nobody will send,
// which is worse than a visible failure.
func TestTheDefaultSenderFailsLoudly(t *testing.T) {
	t.Parallel()
	err := Discard{}.Send(context.Background(), Message{To: "rosa@example.test"})
	if !errors.Is(err, ErrNoSender) {
		t.Fatalf("want ErrNoSender, got %v", err)
	}
}

// Header injection through an address is one of the older ways to turn a
// transactional email into a relay for somebody else's spam.
func TestAddressesThatCouldCarryAHeaderAreRefused(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{
		"rosa@example.test\r\nBcc: everybody@example.test",
		"rosa@example.test\nSubject: something else",
		"rosa@example.test\r",
		"",
		"   ",
	} {
		if err := ValidateAddress(addr); err == nil {
			t.Errorf("accepted %q", addr)
		}
	}
	if err := ValidateAddress("rosa@example.test"); err != nil {
		t.Errorf("refused an ordinary address: %v", err)
	}
}

func TestRenderedMessagesArePlainTextWithNoTracking(t *testing.T) {
	t.Parallel()
	rendered := Render("amici@example.test", Message{
		To:      "rosa@example.test",
		Subject: "Confirm your email address",
		Body:    "Hello Rosa,\n\nhttps://amici.test/confirm-email?token=abc\n",
	}, time.Date(2026, 3, 14, 10, 30, 0, 0, time.UTC))

	headers, body, found := strings.Cut(rendered, "\r\n\r\n")
	if !found {
		t.Fatalf("the message has no body:\n%s", rendered)
	}
	for _, want := range []string{
		"To: rosa@example.test",
		"Subject: Confirm your email address",
		"Content-Type: text/plain; charset=utf-8",
		"Auto-Submitted: auto-generated",
	} {
		if !strings.Contains(headers, want) {
			t.Errorf("missing header %q in:\n%s", want, headers)
		}
	}
	// No HTML part means no remote image and no tracking pixel, which is the
	// whole reason for sending plain text.
	if strings.Contains(rendered, "text/html") || strings.Contains(rendered, "<img") {
		t.Errorf("the message has an HTML part:\n%s", rendered)
	}
	// Every line ends CRLF, because a bare newline in a body is how a message
	// gets mangled by a strict server.
	for _, line := range strings.Split(body, "\r\n") {
		if strings.ContainsAny(line, "\r\n") {
			t.Errorf("a line was not terminated properly: %q", line)
		}
	}
}

// A subject is member-visible text but not member-supplied, so this is a belt
// as well as braces: encoding it means a non-ASCII display name in a subject
// cannot become a second header either.
func TestSubjectsAreEncodedRatherThanInterpolated(t *testing.T) {
	t.Parallel()
	rendered := Render("amici@example.test", Message{
		To:      "rosa@example.test",
		Subject: "Confirm your address for Amici · un posto tra amici",
		Body:    "hello",
	}, time.Now())

	subject := ""
	for _, line := range strings.Split(rendered, "\r\n") {
		if strings.HasPrefix(line, "Subject: ") {
			subject = line
			break
		}
	}
	if subject == "" {
		t.Fatalf("no subject header in:\n%s", rendered)
	}
	if !strings.Contains(subject, "=?utf-8?") {
		t.Errorf("a non-ASCII subject was not encoded: %q", subject)
	}
}

func TestTheOutboxRecordsWhatWouldHaveBeenSent(t *testing.T) {
	t.Parallel()
	out := NewOutbox(slog.New(slog.NewTextHandler(io.Discard, nil)), 3)

	for _, subject := range []string{"one", "two", "three", "four"} {
		if err := out.Send(context.Background(), Message{To: "rosa@example.test", Subject: subject}); err != nil {
			t.Fatalf("send %s: %v", subject, err)
		}
	}

	// Bounded, so a development server left running for a week does not grow
	// without limit.
	got := out.Messages()
	if len(got) != 3 {
		t.Fatalf("holding %d messages, want the limit of 3", len(got))
	}
	if got[0].Subject != "two" || got[2].Subject != "four" {
		t.Errorf("kept the wrong end of the ring: %s to %s", got[0].Subject, got[2].Subject)
	}

	out.Forget()
	if len(out.Messages()) != 0 {
		t.Error("the outbox still has messages after being emptied")
	}
}

func TestAnSMTPSenderWithoutSomewhereToSendIsRefused(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]SMTPConfig{
		"no address":      {From: "amici@example.test"},
		"no port":         {Addr: "smtp.example.test", From: "amici@example.test"},
		"no from address": {Addr: "smtp.example.test:587"},
		"nothing set":     {},
	} {
		if _, err := NewSMTPSender(cfg); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := NewSMTPSender(SMTPConfig{Addr: "smtp.example.test:587", From: "amici@example.test"}); err != nil {
		t.Errorf("a complete configuration was refused: %v", err)
	}
}
