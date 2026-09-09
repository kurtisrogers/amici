// Package web is Amici's HTTP surface: routing, sessions, CSRF, security
// headers and templates.
//
// Handlers in this package parse input, call a service, and render. They hold
// no rules about who may see what. If you find yourself writing an
// authorisation check in here, it belongs in internal/service instead, where
// it can be tested without an HTTP server and where the next person will look
// for it.
package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/mail"
	"github.com/kurtisrogers/amici/internal/service"
)

//go:embed all:templates
var templateFS embed.FS

//go:embed all:static
var staticFS embed.FS

// Options is what the server needs to be built.
type Options struct {
	Config   *config.Config
	Services *service.Services
	Logger   *slog.Logger
	// Fixtures is nil unless this is a binary built with the `fixtures` tag
	// and the configuration allows them. When it is nil the fixture routes
	// are not registered at all, so there is no handler to reach even if the
	// config flag were somehow wrong — and in a release build the handlers do
	// not exist to be registered. See fixtures.go.
	Fixtures *FixtureHooks
	// Outbox is the development mail sender, when one is in use. It is nil in
	// any deployment sending real mail, and the endpoint that reads it is
	// registered on the same terms as the fixture routes.
	Outbox *mail.Outbox
}

// Server holds everything an HTTP handler needs.
type Server struct {
	cfg       *config.Config
	services  *service.Services
	log       *slog.Logger
	templates *template.Template
	static    http.Handler
	handler   http.Handler
	fixtures  *FixtureHooks
	outbox    *mail.Outbox
}

// New builds the server and parses templates. Templates are parsed once at
// startup so that a broken template fails the process rather than a member's
// page view.
func New(opts Options) (*Server, error) {
	cfg, services, log := opts.Config, opts.Services, opts.Logger
	if log == nil {
		log = slog.Default()
	}
	if cfg == nil || services == nil {
		return nil, fmt.Errorf("web: config and services are both required")
	}
	tpl, err := template.New("amici").Funcs(templateFuncs()).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("open static assets: %w", err)
	}

	s := &Server{
		cfg:       cfg,
		services:  services,
		log:       log,
		templates: tpl,
		fixtures:  opts.Fixtures,
		outbox:    opts.Outbox,
	}
	s.static = s.staticHandler(sub)
	s.handler = s.routes()
	return s, nil
}

// ServeHTTP makes the Server an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// staticHandler serves the bundled CSS, JavaScript and stickers.
//
// Assets are embedded in the binary, which means deploying Amici is copying
// one file, and it also means no member request ever reaches a third party for
// a stylesheet or a font.
func (s *Server) staticHandler(assets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Assets are immutable per build in the sense that a change to one
		// changes the binary, so a long cache with revalidation is safe and
		// keeps repeat page loads quiet.
		if s.cfg.IsProduction() {
			w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		// Deliberately relaxing the Cross-Origin-Resource-Policy that
		// secureHeaders sets for everything else.
		//
		// The profile canvas frame is sandboxed without allow-same-origin, so
		// its origin is opaque and a request from it for a sticker is a
		// cross-origin request as far as the browser is concerned.
		// same-origin therefore blocks Amici's own decoration from loading in
		// Amici's own frame, which is how the sticker set came to be broken.
		//
		// Nothing under /static is private: a stylesheet and sixteen
		// decorative SVGs, identical for every member. CORP exists to stop
		// another site embedding a resource whose *content* is a secret, and
		// there is no secret here to protect.
		w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")

		fileServer.ServeHTTP(w, r)
	}))
}

// Run starts the server and blocks until the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       s.cfg.ReadTimeout,
		WriteTimeout:      s.cfg.WriteTimeout,
		IdleTimeout:       s.cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}

	// Housekeeping runs alongside the server: expired sessions and spent
	// invites are deleted rather than left to accumulate. Data you do not
	// hold cannot leak.
	housekeeping, stopHousekeeping := context.WithCancel(ctx)
	defer stopHousekeeping()
	go s.housekeep(housekeeping)

	errs := make(chan error, 1)
	go func() {
		s.log.Info("amici is listening",
			slog.String("addr", s.cfg.Addr),
			slog.String("env", string(s.cfg.Env)),
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errs <- fmt.Errorf("listen on %s: %w", s.cfg.Addr, err)
			return
		}
		errs <- nil
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		s.log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}

// housekeep purges expired records on a timer.
func (s *Server) housekeep(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		// Run once at startup as well, so a long-stopped instance tidies up
		// as soon as it comes back rather than an hour later.
		swept, err := s.services.Accounts.PurgeExpired(ctx)
		if err != nil {
			s.log.Warn("housekeeping failed", "error", err)
		} else if swept.Any() {
			s.log.Info("housekeeping",
				slog.Int("expired_sessions_removed", swept.Sessions),
				slog.Int("spent_invites_removed", swept.Invites),
				slog.Int("spent_links_removed", swept.Tokens),
				slog.Int("rate_limit_windows_removed", swept.RateLimits),
				slog.Int("closed_accounts_deleted", swept.AccountsDeleted),
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
