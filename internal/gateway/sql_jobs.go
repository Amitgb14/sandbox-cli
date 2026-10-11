package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Jobs, secrets and services in the database. A job's runs are rows of
// their own, so a run's progress is written without rewriting the job; a
// service is one JSON document, as the controller reads and writes it
// whole.

func (q *sqlTx) job(ctx context.Context, id string, lock bool) (*jobRecord, bool, error) {
	r := &jobRecord{}
	var created, finished dbTime
	var envSealed []byte
	query := `SELECT r.id, ro.key, ru.name, r.spec, r.env_names, r.env_sealed, r.state, r.error, r.cancelled, r.created, r.finished
		FROM jobs r` + ownerJoin + "WHERE r.id = ?"
	if lock {
		query += q.s.forUpdateOf("r")
	}
	err := q.row(ctx, query, id).Scan(&r.ID, &r.Tenant, &r.User, jsonCol{&r.Spec}, jsonCol{&r.EnvNames}, &envSealed,
		&r.State, &r.Error, &r.Cancelled, &created, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, storeErr(err)
	}
	r.EnvSealed, r.Created, r.Finished = envSealed, created.T, finished.ptr()
	rows, err := q.query(ctx, "SELECT n, run FROM job_runs WHERE job = ? ORDER BY n", id)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var n int
		var run api.JobRun
		if err := rows.Scan(&n, jsonCol{&run}); err != nil {
			return nil, false, storeErr(err)
		}
		if n != len(r.Runs) {
			return nil, false, fmt.Errorf("job %s: run %d stored out of order", id, n)
		}
		r.Runs = append(r.Runs, run)
	}
	return r, true, storeErr(rows.Err())
}

// writeJob writes a job's own columns and every run.
func (q *sqlTx) writeJob(ctx context.Context, r *jobRecord, insert bool) error {
	spec, err := jsonText(r.Spec)
	if err != nil {
		return err
	}
	names, err := jsonText(r.EnvNames)
	if err != nil {
		return err
	}
	if r.EnvNames == nil {
		names = "[]"
	}
	if insert {
		org, user, _, err := q.ownerIDs(ctx, Owner{User: r.User, Tenant: r.Tenant}, true)
		if err != nil {
			return err
		}
		_, err = q.exec(ctx, `INSERT INTO jobs (id, org_id, user_id, spec, env_names, env_sealed, state, error, cancelled, created, finished)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.ID, org, user, spec, names, r.EnvSealed, r.State, r.Error,
			r.Cancelled, q.s.t(r.Created), q.s.tp(r.Finished))
		if err != nil {
			return err
		}
	} else {
		_, err = q.exec(ctx, `UPDATE jobs SET spec = ?, env_names = ?, env_sealed = ?, state = ?, error = ?, cancelled = ?, finished = ?
			WHERE id = ?`, spec, names, r.EnvSealed, r.State, r.Error, r.Cancelled, q.s.tp(r.Finished), r.ID)
	}
	if err != nil {
		return err
	}
	if _, err := q.exec(ctx, "DELETE FROM job_runs WHERE job = ? AND n >= ?", r.ID, len(r.Runs)); err != nil {
		return err
	}
	for n, run := range r.Runs {
		if err := q.writeRun(ctx, r.ID, n, run); err != nil {
			return err
		}
	}
	return nil
}

// writeRun writes run n of job id, in the job's organisation (job_runs
// carries it for row-level security).
func (q *sqlTx) writeRun(ctx context.Context, id string, n int, run api.JobRun) error {
	b, err := jsonText(run)
	if err != nil {
		return err
	}
	var org string
	if err := q.row(ctx, "SELECT org_id FROM jobs WHERE id = ?", id).Scan(&org); err != nil {
		return storeErr(err)
	}
	_, err = q.exec(ctx, `INSERT INTO job_runs (org_id, job, n, run) VALUES (?, ?, ?, ?)
		ON CONFLICT (job, n) DO UPDATE SET run = excluded.run`, org, id, n, b)
	return err
}

func (s *sqlStore) PutJob(ctx context.Context, r *jobRecord) error {
	return s.tx(ctx, func(q *sqlTx) error { return q.writeJob(ctx, r.clone(), true) })
}

func (s *sqlStore) Job(ctx context.Context, id string) (*jobRecord, bool, error) {
	var r *jobRecord
	var ok bool
	err := s.read(ctx, func(q *sqlTx) error {
		var err error
		r, ok, err = q.job(ctx, id, false)
		return err
	})
	return r, ok, err
}

func (s *sqlStore) jobIDs(ctx context.Context, where string, args ...any) ([]string, error) {
	var ids []string
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT r.id FROM jobs r"+ownerJoin+where, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return storeErr(err)
			}
			ids = append(ids, id)
		}
		return storeErr(rows.Err())
	})
	return ids, err
}

func (s *sqlStore) jobsWhere(ctx context.Context, where string, args ...any) ([]*jobRecord, error) {
	ids, err := s.jobIDs(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	out := make([]*jobRecord, 0, len(ids))
	for _, id := range ids {
		r, ok, err := s.Job(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *sqlStore) JobsOf(ctx context.Context, user, tenant string) ([]*jobRecord, error) {
	return s.jobsWhere(ctx, "WHERE ru.name = ? AND ro.key = ? ORDER BY r.created, r.id", user, orgKeyOf(tenant))
}

func (s *sqlStore) AllJobs(ctx context.Context) ([]*jobRecord, error) {
	return s.jobsWhere(ctx, "ORDER BY r.created, r.id")
}

// RunningJobs is the owner of every job still running and not cancelled.
func (s *sqlStore) RunningJobs(ctx context.Context) (map[string]Owner, error) {
	out := map[string]Owner{}
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT r.id, ru.name, ro.key FROM jobs r"+ownerJoin+"WHERE r.state = ? AND NOT r.cancelled", api.JobRunning)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var o Owner
			if err := rows.Scan(&id, &o.User, &o.Tenant); err != nil {
				return storeErr(err)
			}
			out[id] = o
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) UpdateJob(ctx context.Context, id string, fn func(*jobRecord)) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, ok, err := q.job(ctx, id, true)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNoSuchJob
		}
		fn(r)
		return q.writeJob(ctx, r, false)
	})
}

func (s *sqlStore) UpdateRun(ctx context.Context, id string, n int, fn func(*api.JobRun)) (api.JobRun, error) {
	var out api.JobRun
	err := s.tx(ctx, func(q *sqlTx) error {
		r, ok, err := q.job(ctx, id, true)
		if err != nil {
			return err
		}
		if !ok || n < 0 || n >= len(r.Runs) {
			return ErrNoSuchJob
		}
		fn(&r.Runs[n])
		out = cloneRun(r.Runs[n])
		return q.writeRun(ctx, id, n, r.Runs[n])
	})
	return out, err
}

func (s *sqlStore) ForgetJob(ctx context.Context, id string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		if _, err := q.exec(ctx, "DELETE FROM job_runs WHERE job = ?", id); err != nil {
			return err
		}
		_, err := q.exec(ctx, "DELETE FROM jobs WHERE id = ?", id)
		return err
	})
}

// --- secrets ---------------------------------------------------------------------

func (s *sqlStore) PutSecret(ctx context.Context, tenant, name string, sealed []byte) error {
	now := s.stamp()
	return s.tx(ctx, func(q *sqlTx) error {
		var exists, n int
		org, _, err := q.orgID(ctx, tenant, true)
		if err != nil {
			return err
		}
		if err := q.row(ctx, "SELECT COUNT(*) FROM secrets WHERE org_id = ? AND name = ?", org, name).Scan(&exists); err != nil {
			return storeErr(err)
		}
		if exists == 0 {
			if err := q.row(ctx, "SELECT COUNT(*) FROM secrets WHERE org_id = ?", org).Scan(&n); err != nil {
				return storeErr(err)
			}
			if n >= maxSecrets {
				return fmt.Errorf("a tenant holds at most %d secrets", maxSecrets)
			}
		}
		_, err = q.exec(ctx, `INSERT INTO secrets (org_id, name, sealed, updated) VALUES (?, ?, ?, ?)
			ON CONFLICT (org_id, name) DO UPDATE SET sealed = excluded.sealed, updated = excluded.updated`,
			org, name, slices.Clone(sealed), s.t(now))
		return err
	})
}

func (s *sqlStore) SealedSecret(ctx context.Context, tenant, name string) ([]byte, bool, error) {
	var b []byte
	err := s.read(ctx, func(q *sqlTx) error {
		return storeErr(q.row(ctx, "SELECT x.sealed FROM secrets x JOIN orgs o ON o.id = x.org_id WHERE o.key = ? AND x.name = ?",
			orgKeyOf(tenant), name).Scan(&b))
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	return b, err == nil, err
}

func (s *sqlStore) SecretsOf(ctx context.Context, tenant string) ([]api.SecretInfo, error) {
	out := []api.SecretInfo{}
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT x.name, x.updated FROM secrets x JOIN orgs o ON o.id = x.org_id WHERE o.key = ? ORDER BY x.name",
			orgKeyOf(tenant))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var si api.SecretInfo
			var updated dbTime
			if err := rows.Scan(&si.Name, &updated); err != nil {
				return storeErr(err)
			}
			si.Updated = updated.T
			out = append(out, si)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) DeleteSecret(ctx context.Context, tenant, name string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		r, err := q.exec(ctx, "DELETE FROM secrets WHERE org_id = (SELECT id FROM orgs WHERE key = ?) AND name = ?", orgKeyOf(tenant), name)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNoSuchSecret
		}
		return nil
	})
}

// --- services --------------------------------------------------------------------

func (s *sqlStore) Services(ctx context.Context) ([]serviceRecord, error) {
	var out []serviceRecord
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT x.record FROM services x JOIN orgs o ON o.id = x.org_id ORDER BY o.key, x.name")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r serviceRecord
			if err := rows.Scan(jsonCol{&r}); err != nil {
				return storeErr(err)
			}
			out = append(out, r)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

func (s *sqlStore) PutService(ctx context.Context, r serviceRecord) error {
	return s.PutServiceRetiring(ctx, r, nil)
}

func (q *sqlTx) retire(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := q.exec(ctx, "INSERT INTO service_retiring (sandbox) VALUES (?) ON CONFLICT (sandbox) DO NOTHING", id); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqlStore) PutServiceRetiring(ctx context.Context, r serviceRecord, retire []string) error {
	b, err := jsonText(r)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *sqlTx) error {
		org, user, _, err := q.ownerIDs(ctx, Owner{User: r.User, Tenant: r.Tenant}, true)
		if err != nil {
			return err
		}
		if _, err := q.exec(ctx, `INSERT INTO services (org_id, name, user_id, record) VALUES (?, ?, ?, ?)
			ON CONFLICT (org_id, name) DO UPDATE SET user_id = excluded.user_id, record = excluded.record`,
			org, r.Name, user, b); err != nil {
			return err
		}
		return q.retire(ctx, retire)
	})
}

func (s *sqlStore) DeleteService(ctx context.Context, tenant, name string, retire []string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		if _, err := q.exec(ctx, "DELETE FROM services WHERE org_id = (SELECT id FROM orgs WHERE key = ?) AND name = ?",
			orgKeyOf(tenant), name); err != nil {
			return err
		}
		return q.retire(ctx, retire)
	})
}

func (s *sqlStore) Retire(ctx context.Context, ids ...string) error {
	return s.tx(ctx, func(q *sqlTx) error { return q.retire(ctx, ids) })
}

func (s *sqlStore) Retiring(ctx context.Context) ([]string, error) {
	var out []string
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT sandbox FROM service_retiring")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return storeErr(err)
			}
			out = append(out, id)
		}
		return storeErr(rows.Err())
	})
	sort.Strings(out)
	return out, err
}

func (s *sqlStore) Retired(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.tx(ctx, func(q *sqlTx) error {
		for _, id := range ids {
			if _, err := q.exec(ctx, "DELETE FROM service_retiring WHERE sandbox = ?", id); err != nil {
				return err
			}
		}
		return nil
	})
}
