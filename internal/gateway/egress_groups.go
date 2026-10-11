package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Allowlist groups.
//
// Local Studio keeps named sets of hosts — go, npm, an internal registry —
// in its user's own settings, which a gateway's users cannot see. An
// allowlist group is the gateway's version: an admin keeps it, and any user
// names it in a network policy's Groups instead of listing its hosts.
//
//   - The gateway turns the names into hosts before a request reaches a
//     node (expandGroups), so a node sees an allowlist like any other and
//     its may_allow still decides: a group cannot carry a host past a
//     node's policy, and a launch naming one the node would not allow is
//     refused there, as the same host named in allow would be.
//   - Only admins write groups. A group widens what every user who names it
//     reaches, so it is the operator's to say, as a policy is.
//   - An unknown name is refused, never dropped: a sandbox started without
//     a group it named reaches less than asked, and one whose Allow was
//     absent would otherwise fall back to the node's default list.
//   - A change holds for sandboxes created after it. A running sandbox
//     keeps the hosts it was given, so an edit cannot cut off, or widen,
//     sandboxes people are using; a job's or service's next sandbox gets the
//     group as it is then, as it gets a secret's current value.
//   - There is no default group: a launch reaches a group's hosts only by
//     naming it, so an admin's edit never widens a launch that did not ask.

// Errors of the group store.
var (
	ErrNoSuchGroup = errors.New("no such allowlist group")
	ErrGroupInUse  = errors.New("a job or service still names this allowlist group")
)

// checkEgressGroup normalizes a group's hosts as a request's allow list is
// (spec.NormalizeHosts), so a group holds only names a request could, and
// says what else is wrong with it.
func checkEgressGroup(g *api.EgressGroup) error {
	if !policyNameRE.MatchString(g.Name) {
		return errors.New("a group name is 1 to 30 lowercase letters, digits and dashes, starting with a letter")
	}
	if len(g.Description) > 200 {
		return errors.New("description: at most 200 bytes")
	}
	if len(g.Hosts) == 0 {
		return errors.New("a group needs at least one host")
	}
	if len(g.Hosts) > 100 {
		return errors.New("hosts: at most 100")
	}
	hosts, err := spec.NormalizeHosts("host", g.Hosts)
	if err != nil {
		return err
	}
	g.Hosts = hosts
	return nil
}

// --- store ---------------------------------------------------------------------------

func (s *sqlStore) PutEgressGroup(ctx context.Context, g api.EgressGroup) error {
	if err := checkEgressGroup(&g); err != nil {
		return err
	}
	hosts, err := jsonText(g.Hosts)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *sqlTx) error {
		_, err := q.exec(ctx, `INSERT INTO egress_groups (name, description, hosts) VALUES (?, ?, ?)
			ON CONFLICT (name) DO UPDATE SET description = excluded.description, hosts = excluded.hosts`, g.Name, g.Description, hosts)
		return err
	})
}

func (s *sqlStore) DeleteEgressGroup(ctx context.Context, name string) error {
	return s.tx(ctx, func(q *sqlTx) error {
		res, err := q.exec(ctx, "DELETE FROM egress_groups WHERE name = ?", name)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return storeErr(err)
		} else if n == 0 {
			return ErrNoSuchGroup
		}
		return nil
	})
}

func (s *sqlStore) EgressGroups(ctx context.Context) ([]api.EgressGroup, error) {
	var out []api.EgressGroup
	err := s.read(ctx, func(q *sqlTx) error {
		rows, err := q.query(ctx, "SELECT name, description, hosts FROM egress_groups ORDER BY name")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var g api.EgressGroup
			if err := rows.Scan(&g.Name, &g.Description, jsonCol{&g.Hosts}); err != nil {
				return storeErr(err)
			}
			out = append(out, g)
		}
		return storeErr(rows.Err())
	})
	return out, err
}

// --- applying them ---------------------------------------------------------------------

// groupError is a request's groups refused: unknown, or named outside an
// allowlist.
type groupError struct{ msg string }

func (e *groupError) Error() string { return e.msg }

// expandGroups is net with its groups' hosts added to its allow list and no
// groups left, for a node, which knows none. net itself is not changed: it
// may be a job's or a service's spec, which keeps naming the groups so its
// next sandbox gets them as they are then. A nil or group-free net is
// returned as it is. The error is a *groupError for a refusal, or the
// store's.
func (g *Gateway) expandGroups(ctx context.Context, net *api.NetworkPolicy) (*api.NetworkPolicy, error) {
	if net == nil || len(net.Groups) == 0 {
		return net, nil
	}
	if net.Mode != api.NetworkAllowlist {
		return nil, &groupError{fmt.Sprintf("network groups apply to mode %s, not %q", api.NetworkAllowlist, net.Mode)}
	}
	groups, err := g.store.EgressGroups(ctx)
	if err != nil {
		return nil, err
	}
	allow := slices.Clone(net.Allow)
	if allow == nil {
		// Absent, Allow would mean the node's default list; with groups
		// named, the groups' hosts are the list.
		allow = []string{}
	}
	for _, name := range net.Groups {
		i := slices.IndexFunc(groups, func(gr api.EgressGroup) bool { return gr.Name == name })
		if i < 0 {
			return nil, &groupError{"no allowlist group named " + name}
		}
		for _, h := range groups[i].Hosts {
			if !slices.Contains(allow, h) {
				allow = append(allow, h)
			}
		}
	}
	out := *net
	out.Allow, out.Groups = allow, nil
	return &out, nil
}

// checkGroups refuses a job's or service's spec naming groups that cannot
// be expanded, when it is sent rather than at its first sandbox.
func (g *Gateway) checkGroups(ctx context.Context, net *api.NetworkPolicy) error {
	_, err := g.expandGroups(ctx, net)
	return err
}

// groupInUse says whether a job still running or a service names group: its
// next sandbox would fail to be made.
func (g *Gateway) groupInUse(ctx context.Context, group string) (bool, error) {
	names := func(net *api.NetworkPolicy) bool { return net != nil && slices.Contains(net.Groups, group) }
	jobs, err := g.store.AllJobs(ctx)
	if err != nil {
		return false, err
	}
	for _, j := range jobs {
		if j.State == api.JobRunning && names(j.Spec.Network) {
			return true, nil
		}
	}
	svcs, err := g.store.Services(ctx)
	if err != nil {
		return false, err
	}
	for _, s := range svcs {
		if names(s.Spec.Network) || names(s.Serving.Spec.Network) || (s.Target != nil && names(s.Target.Spec.Network)) {
			return true, nil
		}
	}
	return false, nil
}

// --- handlers ----------------------------------------------------------------------------

// listEgressGroups is GET /v1/egress-groups: every key may read the groups,
// to pick from them.
func (g *Gateway) listEgressGroups(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	g.writeEgressGroups(w, r)
}

func (g *Gateway) adminListEgressGroups(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	g.writeEgressGroups(w, r)
}

func (g *Gateway) writeEgressGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := g.store.EgressGroups(r.Context())
	if err != nil {
		g.writeStoreErr(w, err)
		return
	}
	out := api.EgressGroupList{Groups: groups}
	if out.Groups == nil {
		out.Groups = []api.EgressGroup{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminPutEgressGroup(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.EgressGroup
	if !decode(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = r.PathValue("name")
	}
	if req.Name != r.PathValue("name") {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "the name in the body is not the one in the path")
		return
	}
	if err := checkEgressGroup(&req); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	if err := g.store.PutEgressGroup(r.Context(), req); err != nil {
		g.writeStoreErr(w, err)
		return
	}
	g.logf("allowlist group %s set by %s", req.Name, p.KeyID)
	writeJSON(w, http.StatusOK, req)
}

func (g *Gateway) adminDeleteEgressGroup(w http.ResponseWriter, r *http.Request, p Principal) {
	ctx := r.Context()
	if !need(w, p, ScopeAdmin) {
		return
	}
	name := r.PathValue("name")
	if used, err := g.groupInUse(ctx, name); err != nil {
		g.writeStoreErr(w, err)
		return
	} else if used {
		writeErr(w, http.StatusConflict, api.CodeConflict, ErrGroupInUse.Error())
		return
	}
	switch err := g.store.DeleteEgressGroup(ctx, name); {
	case errors.Is(err, ErrNoSuchGroup):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, ErrNoSuchGroup.Error())
	case err != nil:
		g.writeStoreErr(w, err)
	default:
		g.logf("allowlist group %s removed by %s", name, p.KeyID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// applyGroups replaces *net with its groups expanded, for a create or an
// update, or says why it cannot be.
func (g *Gateway) applyGroups(ctx context.Context, net **api.NetworkPolicy) *createFail {
	out, err := g.expandGroups(ctx, *net)
	var ge *groupError
	switch {
	case errors.As(err, &ge):
		return failWith(http.StatusBadRequest, api.CodeInvalidRequest, ge.msg)
	case err != nil:
		return transientFail(http.StatusServiceUnavailable, api.CodeUnavailable, ErrStore.Error())
	}
	*net = out
	return nil
}
