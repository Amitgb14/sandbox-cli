package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// User policies.
//
// A node's policy (sandboxd's policy.yaml) is the same for everyone who
// reaches it. An operator who lets people try the service wants limits per
// person instead — "a demo user's sandbox lives thirty minutes" — without a
// node for each kind of user. A user policy is that: a named set of limits
// in the state file, assigned to users, applied by the gateway on top of
// the nodes' own. It can only narrow what a node allows; the node still
// refuses what it would refuse anyway.
//
// It holds a lifetime only, for now:
//   - At create, a sandbox asking for no lifetime gets the policy's, and one
//     asking for more is refused, as sandboxd refuses one over its limit.
//     Every create goes through createSpread, so jobs' and services' sandboxes
//     are held to their owner's policy too.
//   - Changing a policy holds for sandboxes created after the change. One
//     already running keeps the end it was given, so an edit cannot end, or
//     prolong, the sandboxes people are using under them; the admin changes
//     those one at a time (update.go).
//   - Admin keys are held to no policy: they already act on every sandbox.
//
// A policy is assigned to a user as a person: a user name with the tenant
// of their keys (Principal.KeyTenant), as an organisation's members are
// named, so it follows them into every organisation they act in.

// userPolicyRecord assigns a policy to a user.
type userPolicyRecord struct {
	User   string `json:"user"`
	Tenant string `json:"tenant,omitempty"`
	Policy string `json:"policy"`
}

// Errors of the policy store.
var (
	ErrNoSuchPolicy = errors.New("no such policy")
	ErrPolicyInUse  = errors.New("a user still holds this policy; assign them another first")
)

// checkPolicy says what is wrong with a policy, or nil. Its name is a label
// like an organisation's.
func checkPolicy(p api.UserPolicy) error {
	switch {
	case !orgNameRE.MatchString(p.Name):
		return errors.New("a policy name is 1 to 30 lowercase letters, digits and dashes, starting with a letter")
	case p.MaxLifetimeSecs < 0:
		return errors.New("max_lifetime_secs must not be negative")
	}
	return nil
}

// PutPolicy creates or replaces a policy.
func (s *FileStore) PutPolicy(p api.UserPolicy) error {
	if err := checkPolicy(p); err != nil {
		return err
	}
	return s.change(func() error {
		for i := range s.st.Policies {
			if s.st.Policies[i].Name == p.Name {
				s.st.Policies[i] = p
				return nil
			}
		}
		s.st.Policies = append(s.st.Policies, p)
		return nil
	})
}

// DeletePolicy removes a policy no user holds. One a user holds is refused
// rather than taken from them: removing it would lift their limits, which
// should be asked for by assigning them another, or none.
func (s *FileStore) DeletePolicy(name string) error {
	return s.change(func() error {
		i := slices.IndexFunc(s.st.Policies, func(p api.UserPolicy) bool { return p.Name == name })
		if i < 0 {
			return ErrNoSuchPolicy
		}
		if slices.ContainsFunc(s.st.UserPolicies, func(u userPolicyRecord) bool { return u.Policy == name }) {
			return ErrPolicyInUse
		}
		s.st.Policies = slices.Delete(s.st.Policies, i, i+1)
		return nil
	})
}

// Policies lists the policies by name.
func (s *FileStore) Policies() []api.UserPolicy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := slices.Clone(s.st.Policies)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AssignPolicy gives user, of tenant, the policy name, or none when name is
// "". The policy must exist.
func (s *FileStore) AssignPolicy(user, tenant, name string) error {
	if !ValidUser(user) {
		return fmt.Errorf("user %q: letters, digits and . _ @ + -, at most 64", user)
	}
	if tenant != "" && !ValidUser(tenant) {
		return fmt.Errorf("tenant %q: letters, digits and . _ @ + -, at most 64", tenant)
	}
	return s.change(func() error {
		if name != "" && !slices.ContainsFunc(s.st.Policies, func(p api.UserPolicy) bool { return p.Name == name }) {
			return ErrNoSuchPolicy
		}
		s.st.UserPolicies = slices.DeleteFunc(s.st.UserPolicies, func(u userPolicyRecord) bool { return u.User == user && u.Tenant == tenant })
		if name != "" {
			s.st.UserPolicies = append(s.st.UserPolicies, userPolicyRecord{User: user, Tenant: tenant, Policy: name})
		}
		return nil
	})
}

// PolicyOf is the policy user, of the tenant of their keys, holds.
func (s *FileStore) PolicyOf(user, keyTenant string) (api.UserPolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policyOf(user, keyTenant)
}

// policyOf is PolicyOf under mu.
func (s *FileStore) policyOf(user, keyTenant string) (api.UserPolicy, bool) {
	for _, u := range s.st.UserPolicies {
		if u.User == user && u.Tenant == keyTenant {
			for _, p := range s.st.Policies {
				if p.Name == u.Policy {
					return p, true
				}
			}
		}
	}
	return api.UserPolicy{}, false
}

// PolicyActingIn is the policy of user acting in tenant, for a principal
// that does not carry the tenant of the user's keys: a job's or a
// service's, made from the record it keeps. The person is the one whose
// keys are in tenant or, in an organisation, its member of that name — a
// name an organisation holds once (ErrMemberName).
func (s *FileStore) PolicyActingIn(user, tenant string) (api.UserPolicy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.st.Keys {
		if k.User == user && k.Tenant == tenant {
			return s.policyOf(user, tenant)
		}
	}
	if i := s.orgIndex(tenant); i >= 0 {
		for _, m := range s.st.Orgs[i].Members {
			if m.User == user {
				return s.policyOf(user, m.Tenant)
			}
		}
	}
	return s.policyOf(user, tenant)
}

// policyFor is the policy p's sandboxes are held to; none for an admin.
func (g *Gateway) policyFor(p Principal) (api.UserPolicy, bool) {
	if p.Can(ScopeAdmin) {
		return api.UserPolicy{}, false
	}
	if p.KeyTenant != "" || p.KeyID != "" {
		return g.store.PolicyOf(p.User, p.KeyTenant)
	}
	return g.store.PolicyActingIn(p.User, p.Tenant)
}

// applyPolicyLifetime holds a create's lifetime to the caller's policy:
// none asked for is the policy's, unless a node's own limit is stricter, in
// which case the node gives its own; more than the policy's is refused.
func (g *Gateway) applyPolicyLifetime(p Principal, req *api.CreateSandboxRequest) error {
	pol, ok := g.policyFor(p)
	if !ok || pol.MaxLifetimeSecs == 0 {
		return nil
	}
	switch life := req.LifetimeSecs; {
	case life > pol.MaxLifetimeSecs:
		return fmt.Errorf("lifetime_secs %d: at most %d under policy %s", life, pol.MaxLifetimeSecs, pol.Name)
	case life == 0:
		if caps, ok := g.combinedCapabilities(); ok && caps.Limits.MaxLifetimeSecs > 0 && caps.Limits.MaxLifetimeSecs < pol.MaxLifetimeSecs {
			return nil
		}
		req.LifetimeSecs = pol.MaxLifetimeSecs
	}
	return nil
}

// --- admin --------------------------------------------------------------------

func (g *Gateway) adminListPolicies(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out := api.UserPolicyList{Policies: g.store.Policies()}
	if out.Policies == nil {
		out.Policies = []api.UserPolicy{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) adminPutPolicy(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.UserPolicy
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
	if err := g.store.PutPolicy(req); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	g.logf("policy %s set by %s", req.Name, p.KeyID)
	writeJSON(w, http.StatusOK, req)
}

func (g *Gateway) adminDeletePolicy(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	name := r.PathValue("name")
	switch err := g.store.DeletePolicy(name); {
	case errors.Is(err, ErrNoSuchPolicy):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such policy")
	case errors.Is(err, ErrPolicyInUse):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "removing the policy failed")
	default:
		g.logf("policy %s removed by %s", name, p.KeyID)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (g *Gateway) adminAssignPolicy(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	var req api.AssignPolicyRequest
	if !decode(w, r, &req) {
		return
	}
	if err := g.store.AssignPolicy(req.User, req.Tenant, req.Policy); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	g.logf("policy %q assigned to %s by %s", req.Policy, req.User, p.KeyID)
	w.WriteHeader(http.StatusNoContent)
}
