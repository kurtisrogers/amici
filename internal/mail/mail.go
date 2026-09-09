// Package mail delivers the handful of transactional messages Amici sends.
//
// Two decisions shape everything in here.
//
// First, messages are plain text. No HTML part, no remote images, no tracking
// pixel, no click-wrapped links. A social network's email is normally the most
// heavily instrumented thing it sends you: it is how the big platforms learn
// when you read something and on which device. Amici sends four kinds of
// message, all of them ones you asked for, and none of them tells us that you
// opened it.
//
// Second, delivery sits behind an interface. The service layer composes a
// Message and hands it to a Sender; whether that ends up on a real mail
// server, in a development outbox, or nowhere at all is a wiring decision. So
// no rule about confirming an address has to know that SMTP exists.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Message is one plain-text email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Sender delivers a message. Implementations must be safe for concurrent use.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// ErrNoSender is returned by Discard so that a misconfigured deployment fails
// loudly at the point of sending rather than quietly losing the message.
var ErrNoSender = errors.New("mail: no sender is configured")

// Discard refuses to deliver anything.
//
// It exists so that Sender is never nil, and it returns an error rather than
// swallowing the message: an account confirmation that vanishes silently is
// worse than one that fails visibly, because the member is left waiting for an
// email nobody will ever send.
type Discard struct{}

// Send always fails.
func (Discard) Send(context.Context, Message) error { return ErrNoSender }

// Outbox keeps recent messages in memory instead of delivering them.
//
// This is the development and end-to-end sender. It means the confirmation and
// password reset flows can be driven all the way through by a test, and that
// somebody running Amici on their laptop can follow a confirmation link
// without configuring a mail server first.
//
// It holds a bounded ring of messages so a long-running development server
// cannot grow without limit, and it is only ever wired up when the fixture
// endpoints are enabled, which the config refuses to allow in production.
type Outbox struct {
	log   *slog.Logger
	limit int

	mu       sync.Mutex
	messages []Delivered
}

// Delivered is a message the outbox accepted, with the time it happened.
type Delivered struct {
	Message
	At time.Time
}

// NewOutbox builds an outbox holding at most limit messages.
func NewOutbox(log *slog.Logger, limit int) *Outbox {
	if log == nil {
		log = slog.Default()
	}
	if limit <= 0 {
		limit = 50
	}
	return &Outbox{log: log, limit: limit}
}

// Send records the message and logs it, so a developer watching the server
// output can see the link without opening another window.
func (o *Outbox) Send(_ context.Context, m Message) error {
	o.mu.Lock()
	o.messages = append(o.messages, Delivered{Message: m, At: time.Now().UTC()})
	if len(o.messages) > o.limit {
		o.messages = o.messages[len(o.messages)-o.limit:]
	}
	o.mu.Unlock()

	o.log.Info("mail not sent, recorded in the development outbox",
		slog.String("to", m.To),
		slog.String("subject", m.Subject),
		slog.String("body", m.Body),
	)
	return nil
}

// Messages returns the recorded messages, newest last.
func (o *Outbox) Messages() []Delivered {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Delivered, len(o.messages))
	copy(out, o.messages)
	return out
}

// Forget empties the outbox. The fixture reset calls this so one test's
// messages are not visible to the next.
func (o *Outbox) Forget() {
	o.mu.Lock()
	o.messages = nil
	o.mu.Unlock()
}

// SMTPConfig describes a real mail server.
type SMTPConfig struct {
	// Addr is host:port.
	Addr string
	// From is the envelope and header sender.
	From string
	// Username and Password are optional; leaving them empty sends
	// unauthenticated, which is normal for a relay on localhost.
	Username string
	Password string
	// AllowPlaintext permits delivery over a connection with no TLS. It
	// exists for a relay reachable only over a loopback interface or a
	// private network, and has to be asked for explicitly.
	AllowPlaintext bool
	// Timeout bounds the whole conversation.
	Timeout time.Duration
}

// SMTPSender delivers over SMTP.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender validates the configuration and returns a sender.
func NewSMTPSender(cfg SMTPConfig) (*SMTPSender, error) {
	if cfg.Addr == "" {
		return nil, errors.New("mail: SMTP address is required")
	}
	if _, _, err := net.SplitHostPort(cfg.Addr); err != nil {
		return nil, fmt.Errorf("mail: SMTP address must be host:port: %w", err)
	}
	if cfg.From == "" {
		return nil, errors.New("mail: a from address is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	return &SMTPSender{cfg: cfg}, nil
}

// Send delivers one message, upgrading the connection with STARTTLS.
//
// Refusing to continue in plaintext is deliberate. The messages Amici sends
// are password reset links and address confirmations, so a message read in
// transit is an account taken over. A deployment that genuinely has a relay on
// a trusted interface can say so with AllowPlaintext.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	if err := ValidateAddress(m.To); err != nil {
		return err
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mail: subject contains a newline")
	}
	host, _, err := net.SplitHostPort(s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("mail: parse SMTP address: %w", err)
	}

	dialer := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", s.cfg.Addr, err)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(s.cfg.Timeout)
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: open SMTP session with %s: %w", host, err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: start TLS with %s: %w", host, err)
		}
	} else if !s.cfg.AllowPlaintext {
		return fmt.Errorf("mail: %s does not offer STARTTLS, and plaintext delivery has not been permitted", host)
	}

	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, host)); err != nil {
			return fmt.Errorf("mail: authenticate with %s: %w", host, err)
		}
	}

	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("mail: set sender: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: set recipient: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: open message body: %w", err)
	}
	if _, err := wc.Write([]byte(Render(s.cfg.From, m, time.Now()))); err != nil {
		wc.Close()
		return fmt.Errorf("mail: write message body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("mail: finish message body: %w", err)
	}
	return client.Quit()
}

// Render builds the RFC 5322 message.
//
// Header values are encoded rather than interpolated raw, and anything that
// could introduce a newline is rejected before we get here by ValidateAddress.
// Header injection through a display name is one of the older ways to turn a
// transactional email into a spam relay.
func Render(from string, m Message, at time.Time) string {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + m.To + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	b.WriteString("Date: " + at.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	// Amici's mail is transactional and never a list, but marking it as
	// automatic keeps well-behaved mail servers from replying to it with an
	// out-of-office and keeps it out of anybody's promotions tab.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("X-Auto-Response-Suppress: All\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(normaliseNewlines(m.Body), "\n", "\r\n"))
	return b.String()
}

func normaliseNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// ValidateAddress rejects an address that could carry a header injection.
// Structural validity is somebody else's job; this is only about safety.
func ValidateAddress(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return errors.New("mail: empty address")
	}
	if strings.ContainsAny(addr, "\r\n") {
		return errors.New("mail: address contains a newline")
	}
	return nil
}
