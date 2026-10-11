package gateway

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One gateway serves a SQLite database: a second is refused, and can serve
// once the first lets go. The offline commands need no hold.
func TestOneGatewayServesSQLite(t *testing.T) {
	if os.Getenv("SANDBOX_TEST_POSTGRES_DSN") != "" {
		t.Skip("SQLite's")
	}
	path := filepath.Join(t.TempDir(), "gateway.db")
	a := reopenTestStore(t, path)
	b := reopenTestStore(t, path)
	ctx := context.Background()
	held, err := HoldServing(ctx, a.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HoldServing(ctx, b.Store, nil); err == nil || !strings.Contains(err.Error(), "another gateway serves") {
		t.Fatalf("a second gateway on one SQLite database: %v", err)
	}
	if _, _, err := b.CreateKey("ops", "", []string{ScopeAdmin}); err != nil {
		t.Fatalf("an offline change beside a serving gateway: %v", err)
	}
	held.Release()
	again, err := HoldServing(ctx, b.Store, nil)
	if err != nil {
		t.Fatalf("after the first let go: %v", err)
	}
	again.Release()
}

// On PostgreSQL a second gateway waits, and takes over when the first lets
// go; one whose connection the database drops is told at once.
func TestStandbyTakesOverOnPostgres(t *testing.T) {
	dsn := os.Getenv("SANDBOX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("a standby is PostgreSQL's: set SANDBOX_TEST_POSTGRES_DSN (make test-postgres)")
	}
	path := filepath.Join(t.TempDir(), "gateway.db")
	a := reopenTestStore(t, path)
	b := reopenTestStore(t, path)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	first, err := HoldServing(ctx, a.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	got := make(chan *Serving, 1)
	go func() {
		s, err := HoldServing(ctx, b.Store, func() { close(waited) })
		if err != nil {
			t.Error(err)
		}
		got <- s
	}()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		t.Fatal("the second gateway did not wait")
	}
	select {
	case <-got:
		t.Fatal("the second gateway served while the first did")
	case <-time.After(500 * time.Millisecond):
	}
	first.Release()
	var second *Serving
	select {
	case second = <-got:
	case <-time.After(10 * time.Second):
		t.Fatal("the second gateway did not take over")
	}

	// The database ends the serving gateway's connection, as a failover or
	// an administrator would: it is told, and stops.
	admin, err := sql.Open("pgx", testDatabase(t, dsn, path))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND objid = ($1::bigint & 4294967295)::oid`, int64(serveLockKey)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second.Lost:
		if !strings.Contains(err.Error(), "lost the database's serving lock") {
			t.Errorf("lost: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a gateway whose lock was taken from it was not told")
	}
	second.Release()
}
