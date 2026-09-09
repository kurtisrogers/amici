//go:build !fixtures

package main

import (
	"log/slog"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/web"
)

// fixtureHooks supplies nothing in a release build.
//
// The warning matters. AMICI_ENABLE_FIXTURES defaults to true outside
// production, so somebody who builds without the tag and then wonders why the
// browser suite cannot reset anything deserves to be told why in the first
// line of the log rather than after an hour. A setting that silently does
// nothing is worse than one that refuses.
//
// It warns rather than refusing to start because the flag's default is on
// outside production: a plain `go build` followed by `./amici` is a reasonable
// thing to do, and it should run.
func fixtureHooks(
	cfg *config.Config,
	_ domain.Store,
	_ domain.Clock,
	log *slog.Logger,
) *web.FixtureHooks {
	if cfg.EnableFixtures {
		log.Warn("AMICI_ENABLE_FIXTURES is set, but this binary was built without the " +
			"`fixtures` tag, so the fixture endpoints do not exist. Rebuild with " +
			"`go build -tags fixtures ./cmd/amici` if you wanted them")
	}
	return nil
}
