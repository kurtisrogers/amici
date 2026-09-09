package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// fixtureTables is every table holding member data, ordered so that deleting
// them in sequence never trips a foreign key. Migration bookkeeping is
// deliberately absent: resetting the data must not make the schema look
// unmigrated.
var fixtureTables = []string{
	"reactions",
	"comments",
	"posts",
	"canvases",
	"invites",
	"friend_requests",
	"friendships",
	"blocks",
	"reports",
	"audit_events",
	"recovery_codes",
	"account_tokens",
	"rate_limits",
	"sessions",
	"accounts",
}

// TruncateAll empties every data table, implementing domain.Resettable.
//
// This exists for the fixtures loader and nothing else. It is not reachable
// from any service, because domain.Resettable is a separate interface from
// domain.Store, so no business rule is one type assertion away from being
// able to delete everybody's account. The web endpoint that reaches it is
// registered only when fixtures are enabled, and the configuration refuses to
// enable them in production.
func (s *Store) TruncateAll(ctx context.Context) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, table := range fixtureTables {
			// The table names come from the constant slice above, never from
			// a request, so there is nothing here for an injection to reach.
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table); err != nil {
				return fmt.Errorf("truncate %s: %w", table, err)
			}
		}
		return nil
	})
}
