// Command amici runs the server.
//
// Everything is configured through the environment so that the same binary
// runs on a laptop and in production with no build tags and no config file to
// forget to copy. internal/config documents each variable and refuses to
// start with a combination that would be unsafe, which is why there is so
// little logic here: this file wires five packages together and gets out of
// the way.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/fixtures"
	"github.com/kurtisrogers/amici/internal/mail"
	"github.com/kurtisrogers/amici/internal/service"
	"github.com/kurtisrogers/amici/internal/store/sqlite"
	"github.com/kurtisrogers/amici/internal/web"
)

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet at the point something fails, so the
		// last word goes to stderr.
		fmt.Fprintf(os.Stderr, "amici: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)

	// Cancelled on SIGINT or SIGTERM, which gives in-flight requests the
	// shutdown grace period rather than dropping them.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", cfg.DatabasePath, err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Warn("could not close the database cleanly", "error", err)
		}
	}()

	sender, outbox, err := newMailer(cfg, log)
	if err != nil {
		return err
	}

	clock := domain.SystemClock{}
	services := service.New(service.Deps{
		Store:   store,
		Clock:   clock,
		Logger:  log,
		Secret:  cfg.SecretKey,
		BaseURL: cfg.BaseURL,
		Mailer:  sender,
	})

	opts := web.Options{
		Config:   cfg,
		Services: services,
		Logger:   log,
		Outbox:   outbox,
	}

	// The fixture loader is only handed over when the config allows it, and
	// config.Load will not allow it in production. The web layer therefore
	// has no way to wipe a database unless both of those are true.
	if cfg.EnableFixtures {
		log.Warn("fixtures are enabled: /fixtures/reset will wipe this database",
			slog.String("database", cfg.DatabasePath))
		opts.LoadFixtures = func(ctx context.Context) (*fixtures.Seeded, error) {
			return fixtures.Load(ctx, store, clock, cfg.SecretKey, true)
		}
	}

	srv, err := web.New(opts)
	if err != nil {
		return err
	}

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	log.Info("goodbye")
	return nil
}

// newMailer picks a delivery mechanism.
//
// A configured SMTP relay is used wherever one is given. Without one, and only
// outside production, messages are recorded in an in-memory outbox instead:
// somebody running Amici on a laptop can follow a confirmation link straight
// out of the server log, and the browser suite can read the same messages back
// over an endpoint that only exists when fixtures do. config.Load will not let
// production reach this branch, so a real deployment either has a mail server
// or does not start.
//
// The outbox is returned separately from the Sender because the web layer needs
// the concrete type to read it back, while the service layer only ever needs
// something it can send through.
func newMailer(cfg *config.Config, log *slog.Logger) (mail.Sender, *mail.Outbox, error) {
	if cfg.Mail.Configured() {
		sender, err := mail.NewSMTPSender(mail.SMTPConfig{
			Addr:           cfg.Mail.SMTPAddr,
			From:           cfg.Mail.From,
			Username:       cfg.Mail.Username,
			Password:       cfg.Mail.Password,
			AllowPlaintext: cfg.Mail.AllowPlaintext,
		})
		if err != nil {
			return nil, nil, err
		}
		log.Info("mail will be delivered over SMTP",
			slog.String("relay", cfg.Mail.SMTPAddr),
			slog.String("from", cfg.Mail.From),
		)
		return sender, nil, nil
	}

	log.Warn("no mail server is configured: confirmation and password reset messages " +
		"will be written to the log and kept in memory, not delivered")
	outbox := mail.NewOutbox(log, 50)
	return outbox, outbox, nil
}

// newLogger builds the structured logger.
//
// JSON in production because something is going to collect it, and text on a
// laptop because a person is going to read it.
func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelDebug
	if cfg.IsProduction() {
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	if cfg.IsProduction() {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
