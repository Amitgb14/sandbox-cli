package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// One gateway serves a database at a time.
//
// The gateway keeps state in its own memory as well as in the database:
// names being created, quota in flight, open streams to end on a
// revocation, and the job runner and service controller, which must each
// run once. Two gateways serving one database at once would each hold their
// own, and a revocation made on one would not reach what the other holds
// open. So the database has one serving gateway, and any others wait:
//
//   - On PostgreSQL, serving is holding an advisory lock, on a connection of
//     its own. A second gateway started on the same database waits for it,
//     listening on nothing, so a load balancer's health check finds it down
//     and sends nothing its way. When the serving gateway stops, or its
//     connection to the database is lost, the lock is let go and a waiting
//     gateway takes over, with the whole state.
//   - The serving gateway checks its connection every few seconds; one that
//     cannot is told to stop at once (lost), rather than go on serving while
//     another may already have taken over.
//   - On SQLite, one machine's file, a second gateway is refused.
//
// The offline commands (keys, policies, nodes, store) take no lock: they
// change the database, and a serving gateway reads keys and policies on
// every request.

// serveLockKey is the advisory lock a serving gateway holds.
const serveLockKey = 7316290413

// Serving is a database held for serving.
type Serving struct {
	// Lost is sent to, once, if the hold is lost while serving: the gateway
	// must stop serving at once.
	Lost    <-chan error
	release func()
}

// Release lets the database go, for another gateway to serve.
func (s *Serving) Release() { s.release() }

// HoldServing takes st for serving, waiting while another gateway serves
// it (PostgreSQL), or refusing (SQLite). waiting, when set, is told once
// that this gateway is waiting.
func HoldServing(ctx context.Context, st Store, waiting func()) (*Serving, error) {
	s, ok := st.(*sqlStore)
	if !ok {
		return nil, errors.New("the store cannot be served")
	}
	if s.d == sqliteDialect {
		lock, err := lockFile(s.path + ".serve.lock")
		if err != nil {
			return nil, fmt.Errorf("another gateway serves %s; one gateway serves a SQLite database (use PostgreSQL for a standby)", s.path)
		}
		return &Serving{Lost: make(chan error), release: lock.unlock}, nil
	}
	return s.holdAdvisory(ctx, waiting)
}

func (s *sqlStore) holdAdvisory(ctx context.Context, waiting func()) (*Serving, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", int64(serveLockKey)).Scan(&got); err != nil {
		conn.Close()
		return nil, storeErr(err)
	}
	if !got {
		if waiting != nil {
			waiting()
		}
		// Blocks in the database until the serving gateway lets go, or
		// ctx ends (the query is cancelled).
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", int64(serveLockKey)); err != nil {
			conn.Close()
			return nil, storeErr(err)
		}
	}
	lost := make(chan error, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := s.stillHeld(pctx, conn)
				cancel()
				if err != nil {
					lost <- fmt.Errorf("lost the database's serving lock: %w", err)
					return
				}
			}
		}
	}()
	release := func() {
		select {
		case <-stop:
			return
		default:
		}
		close(stop)
		<-done
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", int64(serveLockKey))
		conn.Close()
	}
	return &Serving{Lost: lost, release: release}, nil
}

// stillHeld asks, on the connection holding it, whether the lock is still
// this session's: a connection the database dropped and the driver remade
// would answer, but without the lock.
func (s *sqlStore) stillHeld(ctx context.Context, conn *sql.Conn) error {
	var n int
	err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_locks WHERE locktype = 'advisory' AND granted
		AND pid = pg_backend_pid() AND classid = ($1::bigint >> 32)::oid AND objid = ($1::bigint & 4294967295)::oid`,
		int64(serveLockKey)).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return errors.New("the lock is no longer this gateway's")
	}
	return nil
}
