//go:build fixtures

package main

import (
	"context"
	"log/slog"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/fixtures"
	"github.com/kurtisrogers/amici/internal/web"
)

// fixtureHooks wires the fixture world into the HTTP layer.
//
// This file, and the corresponding one in internal/web, are the only places
// internal/fixtures is reachable from the server binary, and both are behind
// the `fixtures` tag. A binary built without it does not contain the fixture
// cast, the shared password, or the handlers that would hand either out.
//
// The translation from fixtures.Seeded into web.FixtureWorld happens here
// because wiring is this file's job: it keeps internal/web free of any
// dependency on the package that builds development data.
func fixtureHooks(
	cfg *config.Config,
	store domain.Store,
	clock domain.Clock,
	log *slog.Logger,
) *web.FixtureHooks {
	if !cfg.EnableFixtures {
		return nil
	}
	log.Warn("fixtures are enabled: /fixtures/reset will wipe this database",
		slog.String("database", cfg.DatabasePath))

	return &web.FixtureHooks{
		Rebuild: func(ctx context.Context) (*web.FixtureWorld, error) {
			seeded, err := fixtures.Load(ctx, store, clock, cfg.SecretKey, true)
			if err != nil {
				return nil, err
			}
			world := describeCast()
			world.InviteCodes = seeded.InviteCodes
			world.Posts = seeded.Posts
			world.AccountIDs = make(map[string]string, len(seeded.Accounts))
			for handle, acct := range seeded.Accounts {
				world.AccountIDs[handle] = string(acct.ID)
			}
			return world, nil
		},
		Describe: describeCast,
	}
}

// describeCast reports who is in the fixture world without touching the
// database.
func describeCast() *web.FixtureWorld {
	people := make([]web.FixturePerson, 0, len(fixtures.People))
	for _, p := range fixtures.People {
		people = append(people, web.FixturePerson{
			Handle:           p.Handle,
			DisplayName:      p.DisplayName,
			Email:            p.Email,
			Role:             string(p.Role),
			ReachableByEmail: p.ReachableByEmail,
			EmailConfirmed:   !p.Unconfirmed,
			Note:             p.Note,
		})
	}
	return &web.FixtureWorld{Password: fixtures.Password, People: people}
}
