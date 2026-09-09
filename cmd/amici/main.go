// Command amici runs the server.
//
// Everything a deployment needs to vary is configured through the environment,
// so the artifact that runs in production is the artifact that was tested.
// internal/config documents each variable and refuses to start with a
// combination that would be unsafe, which is why there is so little logic
// here: this file wires six packages together and gets out of the way.
//
// There is exactly one build tag, `fixtures`, and it only ever removes things.
// It gates the development-only endpoints that rebuild the fixture world and
// read back the outbox, because one of those wipes the database and the other
// hands out live password reset links. Every rule about who may see what is
// identical in both builds; see docs/deployment.md for why that line is drawn
// at the build rather than at a configuration flag.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/mail"
	"github.com/kurtisrogers/amici/internal/service"
	"github.com/kurtisrogers/amici/internal/store/sqlite"
	"github.com/kurtisrogers/amici/internal/web"
)

// version is stamped at build time by the release workflow:
//
//	-ldflags "-X main.version=v1.2.3"
//
// It is "dev" in anything built without that, which is the honest answer for
// a binary somebody compiled themselves. Knowing exactly what is deployed is
// the difference between reading a bug report and guessing at one.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(versionString())
		return
	}
	if err := run(); err != nil {
		// The logger may not exist yet at the point something fails, so the
		// last word goes to stderr.
		fmt.Fprintf(os.Stderr, "amici: %v\n", err)
		os.Exit(1)
	}
}

// versionString describes this binary.
//
// The VCS revision comes from the Go toolchain's own build info rather than
// another ldflag, so it is right even for a binary somebody built by hand,
// which is exactly the binary whose provenance is least obvious later.
func versionString() string {
	revision, modified := "unknown", false
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	if len(revision) > 12 {
		revision = revision[:12]
	}
	if modified {
		revision += "-dirty"
	}
	return fmt.Sprintf("amici %s (%s, %s/%s, %s)",
		version, revision, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	// First line in the log, so that a report of odd behaviour can always be
	// tied to a specific build without asking.
	log.Info("starting", slog.String("version", versionString()), slog.String("env", string(cfg.Env)))

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

	// The fixture hooks are only supplied by a binary built with the
	// `fixtures` tag, and only when the config allows them. A release binary
	// has no code capable of wiping a database, whatever it is configured to
	// do. See fixtures_on.go and fixtures_off.go.
	srv, err := web.New(web.Options{
		Config:   cfg,
		Services: services,
		Logger:   log,
		Outbox:   outbox,
		Fixtures: fixtureHooks(cfg, store, clock, log),
	})
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
