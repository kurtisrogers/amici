// Command amiciseed fills a database with the fixture world.
//
// It is the same code path the /fixtures/reset endpoint uses, so what a
// developer sees locally and what the browser suite asserts against cannot
// drift apart. It is destructive by design: every table is emptied first.
//
//	go run ./cmd/amiciseed
//	go run ./cmd/amiciseed -db /tmp/amici-test.db
//
// It refuses to run against a production configuration, because a seed script
// that can be pointed at production is a seed script that eventually is.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/fixtures"
	"github.com/kurtisrogers/amici/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "amiciseed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dbPath := flag.String("db", "", "database file (defaults to AMICI_DB, then amici.db)")
	quiet := flag.Bool("quiet", false, "only report failures")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return fmt.Errorf("refusing to seed a production configuration (AMICI_ENV=%s)", cfg.Env)
	}
	path := cfg.DatabasePath
	if *dbPath != "" {
		path = *dbPath
	}

	ctx := context.Background()
	store, err := sqlite.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("open database %s: %w", path, err)
	}
	defer store.Close()

	seeded, err := fixtures.Load(ctx, store, domain.SystemClock{}, cfg.SecretKey, true)
	if err != nil {
		return err
	}
	if *quiet {
		return nil
	}

	fmt.Printf("Seeded %s\n\n", path)
	fmt.Printf("Everybody's password is: %s\n\n", fixtures.Password)

	fmt.Println("Who is who:")
	for _, p := range fixtures.People {
		reach := "codes only"
		if p.ReachableByEmail {
			reach = "reachable by email"
		}
		fmt.Printf("  %-14s %-22s %-10s %-18s %s\n",
			"@"+p.Handle, p.Email, p.Role, reach, p.Note)
	}

	if len(seeded.InviteCodes) > 0 {
		fmt.Println("\nLive request codes:")
		owners := make([]string, 0, len(seeded.InviteCodes))
		for owner := range seeded.InviteCodes {
			owners = append(owners, owner)
		}
		sort.Strings(owners)
		for _, owner := range owners {
			fmt.Printf("  %-14s %s\n", owner, seeded.InviteCodes[owner])
		}
	}

	fmt.Printf("\n%d posts. Start the server with: go run ./cmd/amici\n", seeded.Posts)
	return nil
}
