package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The super admin's dashboard: what the gateway holds, in totals.
//
// The super admin runs the gateway and does not see into other users'
// sandboxes (mayAct): this answers how many there are and what they take,
// never which they are or whose. Every number is a count or a sum.

// statCounts is what the store counts for GET /v1/admin/stats.
type statCounts struct {
	Users, Accounts, SignedIn24h, Suspended int
	Orgs                                    int
	Keys, PersonalKeys                      int
	Running, Created24h, Created7d, Created int
	Allocated                               api.NodeResources
	JobsRunning, Jobs, Services             int
}

func (s *sqlStore) StatCounts(ctx context.Context) (statCounts, error) {
	var c statCounts
	now := s.stamp()
	t := s.t(now)
	day, week := s.t(now.Add(-24*time.Hour)), s.t(now.Add(-7*24*time.Hour))
	err := s.read(ctx, func(q *sqlTx) error {
		// A user is one holding a working key of their own: an account, or
		// a key an admin issued. Sign-ins and personal keys are theirs.
		const live = "NOT k.revoked AND (k.expires IS NULL OR k.expires > ?)"
		for _, r := range []struct {
			dst   *int
			query string
			args  []any
		}{
			{&c.Users, "SELECT COUNT(*) FROM users u WHERE EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.parent IS NULL AND " + live + ")", []any{t}},
			{&c.Accounts, "SELECT COUNT(*) FROM users u WHERE u.email IS NOT NULL AND EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = u.id AND k.account AND " + live + ")", []any{t}},
			{&c.SignedIn24h, "SELECT COUNT(*) FROM users WHERE last_login > ?", []any{day}},
			{&c.Suspended, "SELECT COUNT(*) FROM api_keys WHERE parent IS NULL AND revoked AND restore_until > ?", []any{t}},
			{&c.Orgs, "SELECT COUNT(*) FROM orgs WHERE kind = ?", []any{orgTeam}},
			{&c.Keys, "SELECT COUNT(*) FROM api_keys k WHERE k.parent IS NULL AND " + live, []any{t}},
			{&c.PersonalKeys, "SELECT COUNT(*) FROM api_keys k WHERE k.personal AND " + live, []any{t}},
			{&c.Running, "SELECT COUNT(*) FROM sandboxes WHERE terminated_at IS NULL", nil},
			{&c.Created24h, "SELECT COUNT(*) FROM sandboxes WHERE COALESCE(created_at, recorded) > ?", []any{day}},
			{&c.Created7d, "SELECT COUNT(*) FROM sandboxes WHERE COALESCE(created_at, recorded) > ?", []any{week}},
			{&c.Created, "SELECT COUNT(*) FROM sandboxes", nil},
			{&c.JobsRunning, "SELECT COUNT(*) FROM jobs WHERE state = ?", []any{string(api.JobRunning)}},
			{&c.Jobs, "SELECT COUNT(*) FROM jobs", nil},
			{&c.Services, "SELECT COUNT(*) FROM services", nil},
		} {
			if err := q.row(ctx, r.query, r.args...).Scan(r.dst); err != nil {
				return storeErr(err)
			}
		}
		var cpus float64
		var mem, disk int64
		if err := q.row(ctx, "SELECT COALESCE(SUM(cpus), 0), COALESCE(SUM(memory_mb), 0), COALESCE(SUM(disk_mb), 0) FROM sandboxes WHERE terminated_at IS NULL").
			Scan(&cpus, &mem, &disk); err != nil {
			return storeErr(err)
		}
		c.Allocated = api.NodeResources{CPUs: cpus, MemoryMB: int(mem), DiskMB: int(disk)}
		return nil
	})
	return c, err
}

// adminStats is GET /v1/admin/stats.
func (g *Gateway) adminStats(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	c, err := g.store.StatCounts(r.Context())
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	orphans, err := g.orphanedSandboxes(r.Context())
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	out := api.GatewayStats{
		Time:      time.Now().UTC(),
		Users:     api.UserStats{Total: c.Users, Accounts: c.Accounts, SignedIn24h: c.SignedIn24h, Suspended: c.Suspended},
		Orgs:      c.Orgs,
		Keys:      api.KeyStats{Active: c.Keys, Personal: c.PersonalKeys},
		Sandboxes: api.SandboxStats{Running: c.Running, Created24h: c.Created24h, Created7d: c.Created7d, Created: c.Created, HistoryDays: int(g.cfg.SandboxHistory / (24 * time.Hour)), Orphaned: len(orphans)},
		Jobs:      api.JobStats{Running: c.JobsRunning, Total: c.Jobs},
		Services:  c.Services,
		Allocated: c.Allocated,
	}
	// Capacity and what is given out, from each node's last status: the
	// nodes' own account, which counts sandboxes the store may not (a
	// node's pool, one made outside the gateway).
	for _, n := range g.nodes.list() {
		info := n.info()
		out.Nodes.Total++
		if info.Healthy {
			out.Nodes.Healthy++
		}
		st := info.Status
		if st == nil {
			continue
		}
		if st.Cordoned {
			out.Nodes.Cordoned++
		}
		out.Nodes.Capacity.CPUs += st.Capacity.CPUs
		out.Nodes.Capacity.MemoryMB += st.Capacity.MemoryMB
		out.Nodes.Capacity.DiskMB += st.Capacity.DiskMB
		out.Nodes.Used.CPUs += st.Capacity.CPUs - st.Free.CPUs
		out.Nodes.Used.MemoryMB += st.Capacity.MemoryMB - st.Free.MemoryMB
		out.Nodes.Used.DiskMB += st.Capacity.DiskMB - st.Free.DiskMB
		out.Nodes.Running += st.Running
	}
	writeJSON(w, http.StatusOK, out)
}
