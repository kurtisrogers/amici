// Command amiciadmin grants and removes support and developer access.
//
// This is deliberately a command line tool and not a screen. There is nowhere
// in Amici's interface that can hand out power over other people's accounts,
// which means an attacker who takes over a support session cannot use it to
// make more support accounts, and a developer console compromise cannot
// escalate itself. Changing what an account is allowed to do requires shell
// access to the machine holding the database.
//
//	go run ./cmd/amiciadmin -handle marco -role support
//	go run ./cmd/amiciadmin -handle marco -role member -reason "left the team"
//
// Every change is written to the audit log, where the support and developer
// consoles will show it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kurtisrogers/amici/internal/config"
	"github.com/kurtisrogers/amici/internal/domain"
	"github.com/kurtisrogers/amici/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "amiciadmin: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	handle := flag.String("handle", "", "the account to change, without the @")
	roleName := flag.String("role", "", "member, support or developer")
	reason := flag.String("reason", "", "why, for the audit record")
	dbPath := flag.String("db", "", "database file (defaults to AMICI_DB, then amici.db)")
	flag.Parse()

	if *handle == "" || *roleName == "" {
		flag.Usage()
		return errors.New("both -handle and -role are required")
	}
	role, err := domain.ParseRole(strings.ToLower(*roleName))
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
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

	acct, err := store.AccountByHandle(ctx, domain.NormaliseHandle(*handle))
	if err != nil {
		return fmt.Errorf("find @%s: %w", *handle, err)
	}
	if acct.Role == role {
		fmt.Printf("@%s is already %s. Nothing to do.\n", acct.Handle, role)
		return nil
	}

	was := acct.Role
	acct.Role = role
	if err := store.UpdateAccount(ctx, acct); err != nil {
		return fmt.Errorf("update @%s: %w", acct.Handle, err)
	}

	// The account is recorded as its own actor, with the detail making clear
	// this came from a shell rather than from a person clicking something.
	// Pretending an anonymous operator was a member account would be worse
	// than being plain about it.
	detail := fmt.Sprintf("role changed from %s to %s by amiciadmin", was, role)
	if *reason != "" {
		detail += ": " + *reason
	}
	if err := store.AppendAudit(ctx, &domain.AuditEvent{
		ActorID:   acct.ID,
		Action:    domain.AuditRoleChanged,
		SubjectID: string(acct.ID),
		Detail:    detail,
	}); err != nil {
		return fmt.Errorf("write audit record: %w", err)
	}

	fmt.Printf("@%s is now %s (was %s).\n", acct.Handle, role, was)
	if role != domain.RoleMember {
		fmt.Println("They will need to sign out and back in for the new console to appear.")
	}
	return nil
}
