package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Organisations.
//
// An organisation is a tenant that users made and share, rather than one an
// operator wrote on their keys. It is the same tenant every other part of the
// gateway keys on — ownership (mayAct), listings, quotas, secrets, jobs,
// services and the router's <service>--<tenant> name — so isolating one from
// another needs nothing new there. What is new is choosing the tenant per
// request: a request may send X-Sandbox-Org, and the auth middleware (guard)
// replaces the principal's Tenant with it once, before any handler runs,
// after checking that the key's user is a member. Every handler downstream,
// the router, the live-request registry and the audit record see only the
// chosen tenant.
//
// Who may select what is the whole of the security of this, so it is narrow:
//   - The key's own tenant is always allowed: a request with no header, or
//     one naming it, is exactly what it was before organisations.
//   - Any other tenant needs a membership, and memberships come from two
//     places only: creating an organisation (its creator becomes its owner)
//     and being added by one of its owners. A key issued before organisations
//     has none, so it can select nothing it could not reach already.
//   - An organisation's name is unique among organisations and among every
//     tenant already in use — by keys, sandboxes, volumes, secrets, jobs,
//     services — so creating one cannot make its creator a member of a
//     tenant that already holds someone's work.
//   - An admin key may select any organisation, or the default tenant, as it
//     may already act on every sandbox. Scopes never change with the header:
//     a member's key is no more an admin's in an organisation than outside.
//   - Not a member and no such organisation are one answer, 404, so names
//     cannot be probed.
//
// A user name is unique only within a tenant, so a member is a user and the
// tenant of their own keys (Principal.KeyTenant). Within one organisation a
// user name names one person: adding a second "alice" from another tenant is
// refused, because ownership inside the organisation is keyed on the name and
// the two would share each other's sandboxes.
//
// Losing a membership ends what it allowed at once (accessChanged): open
// forwarded requests and SSH connections in that organisation are closed, and
// jobs and services there stop as for a revoked key, because userActive and
// userMaySSH count a membership only while it exists.

// orgRecord is one organisation in the state file.
type orgRecord struct {
	Name            string         `json:"name"`
	Created         time.Time      `json:"created"`
	CreatedBy       string         `json:"created_by"`
	CreatedByTenant string         `json:"created_by_tenant,omitempty"`
	Members         []memberRecord `json:"members"`
}

// memberRecord is one member: a user, named with the tenant of their keys.
type memberRecord struct {
	User   string    `json:"user"`
	Tenant string    `json:"tenant,omitempty"`
	Role   string    `json:"role"`
	Added  time.Time `json:"added"`
}

// orgNameRE is an organisation's name: a DNS label, so it can be the
// router's <service>--<org> host, starting with a letter.
var orgNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// reservedOrgNames may not be created: "default" is the default tenant on
// the wire, and "admin" would read as the operator's.
var reservedOrgNames = []string{api.DefaultOrg, "admin"}

// CheckOrgName says what is wrong with an organisation name, or nil.
func CheckOrgName(name string) error {
	switch {
	case !orgNameRE.MatchString(name):
		return errors.New("an organization name is 1 to 30 lowercase letters, digits and dashes, starting with a letter")
	case strings.HasSuffix(name, "-"):
		return errors.New("an organization name may not end with a dash")
	case strings.Contains(name, "--"):
		// The router splits <service>--<tenant> on the first "--".
		return errors.New(`an organization name may not contain "--"`)
	case slices.Contains(reservedOrgNames, name):
		return fmt.Errorf("%q is reserved", name)
	}
	return nil
}

// wireOrg is a tenant as an organisation name: the default tenant is
// "default".
func wireOrg(tenant string) string {
	if tenant == "" {
		return api.DefaultOrg
	}
	return tenant
}

// tenantOfOrg is wireOrg's inverse.
func tenantOfOrg(name string) string {
	if name == api.DefaultOrg {
		return ""
	}
	return name
}

// Errors of the organisation store.
var (
	ErrNoSuchOrg    = errors.New("no such organization")
	ErrOrgExists    = errors.New("that name is taken")
	ErrOrgCap       = errors.New("this user may create no more organizations")
	ErrLastOwner    = errors.New("an organization keeps at least one owner")
	ErrNoSuchMember = errors.New("no such member")
	// ErrMemberName refuses a second user of one name in an organisation.
	ErrMemberName = errors.New("another user of that name is already in this organization")
)

// --- the store ----------------------------------------------------------------

// orgIndex is the record named name; under mu.
func (s *FileStore) orgIndex(name string) int {
	for i := range s.st.Orgs {
		if s.st.Orgs[i].Name == name {
			return i
		}
	}
	return -1
}

// tenantInUse reports whether name, compared without case, is a tenant that
// anything in the state belongs to; under mu.
func (s *FileStore) tenantInUse(name string) bool {
	eq := func(t string) bool { return t != "" && strings.EqualFold(t, name) }
	for _, k := range s.st.Keys {
		if eq(k.Tenant) {
			return true
		}
	}
	for _, k := range s.st.SSHKeys {
		if eq(k.Tenant) {
			return true
		}
	}
	for _, t := range s.st.Tokens {
		if eq(t.Tenant) {
			return true
		}
	}
	for _, r := range s.st.Sandboxes {
		if eq(r.Tenant) {
			return true
		}
	}
	for _, o := range s.st.Volumes {
		if eq(o.Tenant) {
			return true
		}
	}
	for _, o := range s.st.Snapshots {
		if eq(o.Tenant) {
			return true
		}
	}
	for _, r := range s.st.Secrets {
		if eq(r.Tenant) {
			return true
		}
	}
	for _, j := range s.st.Jobs {
		if eq(j.Tenant) {
			return true
		}
	}
	if s.st.Services != nil {
		for _, r := range s.st.Services.Records {
			if eq(r.Tenant) {
				return true
			}
		}
	}
	for _, o := range s.st.Orgs {
		if strings.EqualFold(o.Name, name) {
			return true
		}
	}
	return false
}

// nameTakenInOrg refuses user of userTenant joining organisation org when
// another user of the same name is in it already: a member from another
// tenant, or a key whose own tenant is org. Under mu.
func (s *FileStore) nameTakenInOrg(org, user, userTenant string) error {
	if org == "" {
		return nil
	}
	if i := s.orgIndex(org); i >= 0 {
		for _, m := range s.st.Orgs[i].Members {
			if m.User == user && m.Tenant != userTenant {
				return ErrMemberName
			}
		}
	}
	if userTenant != org {
		for _, k := range s.st.Keys {
			if k.Tenant == org && k.User == user {
				return ErrMemberName
			}
		}
	}
	return nil
}

// CreateOrg makes organisation name with user (of userTenant) its owner.
// maxOwned, when positive, is how many organisations one user may have made
// or own.
func (s *FileStore) CreateOrg(name, user, userTenant string, maxOwned int) (orgRecord, error) {
	if err := CheckOrgName(name); err != nil {
		return orgRecord{}, err
	}
	now := s.now().UTC()
	rec := orgRecord{Name: name, Created: now, CreatedBy: user, CreatedByTenant: userTenant,
		Members: []memberRecord{{User: user, Tenant: userTenant, Role: api.RoleOwner, Added: now}}}
	err := s.change(func() error {
		if s.tenantInUse(name) {
			return ErrOrgExists
		}
		if maxOwned > 0 && s.ownedBy(user, userTenant) >= maxOwned {
			return ErrOrgCap
		}
		s.st.Orgs = append(s.st.Orgs, rec)
		return nil
	})
	if err != nil {
		return orgRecord{}, err
	}
	return rec, nil
}

// ownedBy counts the organisations user made or owns: what bounds how many
// quotas one user can hold. Made counts as well as owns, so handing an
// organisation to someone else does not free a place for another. Under mu.
func (s *FileStore) ownedBy(user, userTenant string) int {
	n := 0
	for _, o := range s.st.Orgs {
		mine := o.CreatedBy == user && o.CreatedByTenant == userTenant
		for _, m := range o.Members {
			if m.User == user && m.Tenant == userTenant && m.Role == api.RoleOwner {
				mine = true
			}
		}
		if mine {
			n++
		}
	}
	return n
}

func cloneOrg(o orgRecord) orgRecord {
	o.Members = slices.Clone(o.Members)
	return o
}

// Org returns organisation name.
func (s *FileStore) Org(name string) (orgRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i := s.orgIndex(name); i >= 0 {
		return cloneOrg(s.st.Orgs[i]), true
	}
	return orgRecord{}, false
}

// Orgs lists every organisation by name.
func (s *FileStore) Orgs() []orgRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]orgRecord, 0, len(s.st.Orgs))
	for _, o := range s.st.Orgs {
		out = append(out, cloneOrg(o))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MemberRole implements Store.
func (s *FileStore) MemberRole(org, user, userTenant string) (string, bool) {
	if org == "" || user == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.memberRole(org, user, userTenant)
}

// memberRole is MemberRole under mu.
func (s *FileStore) memberRole(org, user, userTenant string) (string, bool) {
	i := s.orgIndex(org)
	if i < 0 {
		return "", false
	}
	for _, m := range s.st.Orgs[i].Members {
		if m.User == user && m.Tenant == userTenant {
			return m.Role, true
		}
	}
	return "", false
}

// orgMembership is one organisation a user is in.
type orgMembership struct {
	Org     string
	Role    string
	Created time.Time
}

// MembershipsOf lists the organisations user (of userTenant) is in, by name.
func (s *FileStore) MembershipsOf(user, userTenant string) []orgMembership {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []orgMembership
	for _, o := range s.st.Orgs {
		for _, m := range o.Members {
			if m.User == user && m.Tenant == userTenant {
				out = append(out, orgMembership{Org: o.Name, Role: m.Role, Created: o.Created})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Org < out[j].Org })
	return out
}

func owners(ms []memberRecord) int {
	n := 0
	for _, m := range ms {
		if m.Role == api.RoleOwner {
			n++
		}
	}
	return n
}

// SetMember adds user (of userTenant) to org with role, or changes their
// role. It returns the role they held before, "" for a new member. Demoting
// the last owner is refused.
func (s *FileStore) SetMember(org, user, userTenant, role string) (string, error) {
	prev := ""
	err := s.change(func() error {
		i := s.orgIndex(org)
		if i < 0 {
			return ErrNoSuchOrg
		}
		o := &s.st.Orgs[i]
		for j := range o.Members {
			m := &o.Members[j]
			if m.User != user || m.Tenant != userTenant {
				continue
			}
			prev = m.Role
			if m.Role == api.RoleOwner && role != api.RoleOwner && owners(o.Members) == 1 {
				return ErrLastOwner
			}
			m.Role = role
			return nil
		}
		if err := s.nameTakenInOrg(org, user, userTenant); err != nil {
			return err
		}
		o.Members = append(o.Members, memberRecord{User: user, Tenant: userTenant, Role: role, Added: s.now().UTC()})
		return nil
	})
	return prev, err
}

// RemoveMember takes user (of userTenant) out of org. Removing the last
// owner is refused.
func (s *FileStore) RemoveMember(org, user, userTenant string) error {
	return s.change(func() error {
		i := s.orgIndex(org)
		if i < 0 {
			return ErrNoSuchOrg
		}
		o := &s.st.Orgs[i]
		for j, m := range o.Members {
			if m.User != user || m.Tenant != userTenant {
				continue
			}
			if m.Role == api.RoleOwner && owners(o.Members) == 1 {
				return ErrLastOwner
			}
			o.Members = slices.Delete(o.Members, j, j+1)
			return nil
		}
		return ErrNoSuchMember
	})
}

// --- choosing the organisation of a request -------------------------------------

// selectOrg applies X-Sandbox-Org to an authenticated principal: the tenant
// every handler will act in. It answers the request itself, and returns
// false, when the header names nothing the key may select.
func (g *Gateway) selectOrg(w http.ResponseWriter, r *http.Request, p Principal) (Principal, bool) {
	vals := r.Header.Values(api.OrgHeader)
	if len(vals) == 0 {
		return p, true
	}
	if len(vals) > 1 {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, api.OrgHeader+" is given more than once")
		return Principal{}, false
	}
	name := strings.TrimSpace(vals[0])
	if name == "" {
		return p, true
	}
	t := tenantOfOrg(name)
	if t == p.KeyTenant {
		return p, true
	}
	allowed := false
	if p.Can(ScopeAdmin) {
		// An admin acts on every sandbox already; it may act in any
		// organisation that exists, or the default tenant.
		_, exists := g.store.Org(t)
		allowed = t == "" || exists
	} else {
		_, allowed = g.store.MemberRole(t, p.User, p.KeyTenant)
	}
	if !allowed {
		// The same answer for an organisation that does not exist and one
		// the key's user is not in.
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such organization")
		return Principal{}, false
	}
	p.Tenant = t
	return p, true
}

// stillInTenant reports whether a principal chosen with X-Sandbox-Org may
// still act in its tenant: k is the principal's key as the store holds it
// now. The key's own tenant always passes; another needs the membership, or
// an admin key.
func stillInTenant(st Store, p Principal, k Key) bool {
	if p.Tenant == k.Tenant {
		return true
	}
	if (Principal{Scopes: k.Scopes}).Can(ScopeAdmin) {
		return true
	}
	_, ok := st.MemberRole(p.Tenant, k.User, k.Tenant)
	return ok
}

// --- handlers -------------------------------------------------------------------

func (g *Gateway) listOrgs(w http.ResponseWriter, r *http.Request, p Principal) {
	out := api.OrgList{Orgs: []api.Org{}}
	seen := map[string]bool{}
	own := api.Org{Name: wireOrg(p.KeyTenant), Role: api.RoleMember, Current: p.Tenant == p.KeyTenant}
	if rec, ok := g.store.Org(p.KeyTenant); ok {
		own.Created = rec.Created
	}
	if role, ok := g.store.MemberRole(p.KeyTenant, p.User, p.KeyTenant); ok {
		own.Role = role
	}
	out.Orgs = append(out.Orgs, own)
	seen[p.KeyTenant] = true
	for _, m := range g.store.MembershipsOf(p.User, p.KeyTenant) {
		if seen[m.Org] {
			continue
		}
		seen[m.Org] = true
		out.Orgs = append(out.Orgs, api.Org{Name: m.Org, Role: m.Role, Created: m.Created, Current: p.Tenant == m.Org})
	}
	if !seen[p.Tenant] {
		// An admin's key selecting an organisation it is not in.
		o := api.Org{Name: wireOrg(p.Tenant), Role: api.RoleMember, Current: true}
		if rec, ok := g.store.Org(p.Tenant); ok {
			o.Created = rec.Created
		}
		out.Orgs = append(out.Orgs, o)
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) createOrg(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeOrgCreate) {
		return
	}
	var req api.CreateOrgRequest
	if !decode(w, r, &req) {
		return
	}
	if err := CheckOrgName(req.Name); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return
	}
	rec, err := g.store.CreateOrg(req.Name, p.User, p.KeyTenant, g.cfg.MaxOrgsPerUser)
	switch {
	case errors.Is(err, ErrOrgExists):
		writeErr(w, http.StatusConflict, api.CodeConflict, "the name "+req.Name+" is taken")
		return
	case errors.Is(err, ErrOrgCap):
		writeErr(w, http.StatusForbidden, api.CodeRefused,
			fmt.Sprintf("a user may make or own at most %d organizations on this gateway", g.cfg.MaxOrgsPerUser))
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "recording the organization failed")
		return
	}
	g.logf("organization %s created by key %s", rec.Name, p.KeyID)
	g.audit.write(api.AuditEntry{Kind: "org", Action: "org.created", KeyID: p.KeyID, User: p.User,
		Tenant: rec.Name, Remote: remoteIP(r.RemoteAddr), Target: rec.Name, Result: "ok"})
	writeJSON(w, http.StatusCreated, api.Org{Name: rec.Name, Role: api.RoleOwner, Created: rec.Created, Current: p.Tenant == rec.Name})
}

// orgFor finds the organisation a members route names, for a caller who may
// see it (any member, or an admin) and, when manage is set, change it (an
// owner, or an admin). Someone who is not a member gets the 404 of an
// organisation that does not exist; a member who is not an owner a 403.
func (g *Gateway) orgFor(w http.ResponseWriter, r *http.Request, p Principal, manage bool) (orgRecord, bool) {
	name := r.PathValue("name")
	rec, ok := g.store.Org(tenantOfOrg(name))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such organization")
		return orgRecord{}, false
	}
	if p.Can(ScopeAdmin) {
		return rec, true
	}
	role, member := g.store.MemberRole(rec.Name, p.User, p.KeyTenant)
	if !member && p.KeyTenant == rec.Name {
		// A key issued into the organisation's tenant is in it.
		role, member = api.RoleMember, true
	}
	if !member {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such organization")
		return orgRecord{}, false
	}
	if manage && role != api.RoleOwner {
		writeErr(w, http.StatusForbidden, api.CodeRefused, "only an owner of "+rec.Name+" may change its members")
		return orgRecord{}, false
	}
	return rec, true
}

func memberInfo(m memberRecord) api.OrgMember {
	return api.OrgMember{User: m.User, Tenant: m.Tenant, Role: m.Role, Added: m.Added}
}

// memberTenant is the tenant a members request names a user with: the one
// given ("default" for the default tenant), or the caller's own.
func memberTenant(given string, p Principal) string {
	if given == "" {
		return p.KeyTenant
	}
	return tenantOfOrg(given)
}

// memberTarget names a member in the audit record: tenant/user, or the
// user alone for the default tenant. A user name holds no "/".
func memberTarget(user, tenant string) string {
	if tenant == "" {
		return user
	}
	return tenant + "/" + user
}

func (g *Gateway) listOrgMembers(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, ok := g.orgFor(w, r, p, false)
	if !ok {
		return
	}
	out := api.OrgMemberList{Members: []api.OrgMember{}}
	for _, m := range rec.Members {
		out.Members = append(out.Members, memberInfo(m))
	}
	writeJSON(w, http.StatusOK, out)
}

func (g *Gateway) setOrgMember(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, ok := g.orgFor(w, r, p, true)
	if !ok {
		return
	}
	var req api.OrgMemberRequest
	if !decode(w, r, &req) {
		return
	}
	tenant := memberTenant(req.Tenant, p)
	role := req.Role
	if role == "" {
		role = api.RoleMember
	}
	switch {
	case !ValidUser(req.User):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "user: letters, digits and . _ @ + -, at most 64")
		return
	case tenant != "" && !ValidUser(tenant):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "tenant: letters, digits and . _ @ + -, at most 64")
		return
	case role != api.RoleOwner && role != api.RoleMember:
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "role: owner or member")
		return
	}
	prev, err := g.store.SetMember(rec.Name, req.User, tenant, role)
	switch {
	case errors.Is(err, ErrLastOwner), errors.Is(err, ErrMemberName):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case errors.Is(err, ErrNoSuchOrg):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such organization")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "recording the member failed")
		return
	}
	m := api.OrgMember{User: req.User, Tenant: tenant, Role: role}
	if cur, ok := g.store.Org(rec.Name); ok {
		for _, mr := range cur.Members {
			if mr.User == req.User && mr.Tenant == tenant {
				m = memberInfo(mr)
			}
		}
	}
	if prev == role {
		writeJSON(w, http.StatusOK, m)
		return
	}
	action, status := "org.member_added", http.StatusCreated
	if prev != "" {
		action, status = "org.member_role", http.StatusOK
	}
	g.logf("organization %s: %s %s as %s by key %s", rec.Name, action, memberTarget(req.User, tenant), role, p.KeyID)
	g.audit.write(api.AuditEntry{Kind: "org", Action: action, KeyID: p.KeyID, User: p.User, Tenant: rec.Name,
		Remote: remoteIP(r.RemoteAddr), Target: memberTarget(req.User, tenant), Result: role})
	if prev != "" {
		// A role is not access, but what holds a member's access is
		// rechecked on any change to it, so nothing depends on which.
		g.accessChanged()
	}
	writeJSON(w, status, m)
}

func (g *Gateway) removeOrgMember(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, ok := g.orgFor(w, r, p, true)
	if !ok {
		return
	}
	user := r.PathValue("user")
	tenant := memberTenant(r.URL.Query().Get("tenant"), p)
	err := g.store.RemoveMember(rec.Name, user, tenant)
	switch {
	case errors.Is(err, ErrLastOwner):
		writeErr(w, http.StatusConflict, api.CodeConflict, err.Error())
		return
	case errors.Is(err, ErrNoSuchMember), errors.Is(err, ErrNoSuchOrg):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such member")
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "removing the member failed")
		return
	}
	g.logf("organization %s: %s removed by key %s", rec.Name, memberTarget(user, tenant), p.KeyID)
	g.audit.write(api.AuditEntry{Kind: "org", Action: "org.member_removed", KeyID: p.KeyID, User: p.User, Tenant: rec.Name,
		Remote: remoteIP(r.RemoteAddr), Target: memberTarget(user, tenant), Result: "ok"})
	// Before the answer: what the member had open in the organisation ends
	// now, not at the next recheck.
	g.accessChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (g *Gateway) adminListOrgs(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	out := api.AdminOrgList{Orgs: []api.AdminOrg{}}
	for _, o := range g.store.Orgs() {
		out.Orgs = append(out.Orgs, api.AdminOrg{Name: o.Name, Created: o.Created, CreatedBy: o.CreatedBy,
			CreatedByTenant: o.CreatedByTenant, Members: len(o.Members), Owners: owners(o.Members)})
	}
	writeJSON(w, http.StatusOK, out)
}
