package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Services (docs/fleet.md, "Services"): a sandbox spec and a count the
// gateway keeps true. This file is the API; services_controller.go keeps the
// count, services_health.go checks replicas, router_http.go routes traffic.
//
// A service belongs to the user who created it, within their tenant, as a
// sandbox does: another user's service is not found, never forbidden. Its
// name is unique within the tenant, because the router names it by tenant
// (router_http.go), so two users of one tenant cannot both have a "web".
// Its replicas are sandboxes the same user owns, made by the same create
// path as theirs (Gateway.create), counted against the same quota.

// MaxServiceReplicas bounds one service.
const MaxServiceReplicas = 100

// serviceNameRE is a DNS label. "--" is refused anywhere in it: the router
// names a tenant's service <service>--<tenant>, and a service name holding
// "--" could be read as another tenant's.
var serviceNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validServiceName(s string) bool {
	return serviceNameRE.MatchString(s) && !strings.Contains(s, "--")
}

// healthPathRE is what an HTTP health check may ask for: a path, printable,
// no spaces. It is written into a request line, so nothing in it may end
// the line or start another.
var healthPathRE = regexp.MustCompile(`^/[\x21-\x7e]{0,1000}$`)

// errSecretsUnsupported is the answer to a spec naming secrets: the
// gateway has no secret store yet, and a service started without the
// secrets it asked for is not the service that was asked for.
var errSecretsUnsupported = errors.New("secrets need the secret store, which this gateway does not have yet")

// specError is a refused spec, as the HTTP front answers it.
type specError struct {
	status int
	code   string
	msg    string
}

func (e *specError) Error() string { return e.msg }

func badSpec(format string, a ...any) *specError {
	return &specError{http.StatusBadRequest, api.CodeInvalidRequest, fmt.Sprintf(format, a...)}
}

// checkServiceSpec validates a spec and fills in its defaults, so what is
// stored and shown is what is in force. tenant is the owner's: a public
// service must have a name the router can give it.
func checkServiceSpec(sp *api.ServiceSpec, tenant string) *specError {
	if !validServiceName(sp.Name) {
		return badSpec("name %q: a DNS label (lowercase letters, digits and dashes, starting and ending with a letter or digit, at most 63) with no \"--\"", sp.Name)
	}
	if sp.Replicas < 0 || sp.Replicas > MaxServiceReplicas {
		return badSpec("replicas: 0 to %d", MaxServiceReplicas)
	}
	if len(sp.Command) > 256 {
		return badSpec("command: at most 256 arguments")
	}
	for _, a := range sp.Command {
		if strings.ContainsRune(a, 0) {
			return badSpec("command contains a NUL byte")
		}
	}
	r := sp.Resources
	if r.CPUs < 0 || r.MemoryMB < 0 || r.DiskMB < 0 {
		return badSpec("resources cannot be negative")
	}
	if sp.Port < 0 || sp.Port > 65535 {
		return badSpec("port: 1-65535")
	}
	if h := sp.Health; h != nil {
		switch {
		case (h.HTTP == "") == (len(h.Command) == 0):
			return badSpec("health: exactly one of http or command")
		case h.HTTP != "" && !healthPathRE.MatchString(h.HTTP):
			return badSpec("health.http %q: a path starting with /, printable, no spaces", h.HTTP)
		case h.HTTP != "" && sp.Port == 0:
			return badSpec("health.http needs the service's port")
		}
		for _, a := range h.Command {
			if strings.ContainsRune(a, 0) {
				return badSpec("health.command contains a NUL byte")
			}
		}
		if h.EverySecs == 0 {
			h.EverySecs = 10
		}
		if h.TimeoutSecs == 0 {
			h.TimeoutSecs = min(5, h.EverySecs)
		}
		if h.Failures == 0 {
			h.Failures = 3
		}
		switch {
		case h.EverySecs < 1 || h.EverySecs > 3600:
			return badSpec("health.every_secs: 1 to 3600")
		case h.TimeoutSecs < 1 || h.TimeoutSecs > 300:
			return badSpec("health.timeout_secs: 1 to 300")
		case h.Failures < 1 || h.Failures > 100:
			return badSpec("health.failures: 1 to 100")
		}
	}
	if _, err := spec.ResolveEnv(sp.Env); err != nil {
		var ref *spec.Refused
		if errors.As(err, &ref) {
			return &specError{http.StatusForbidden, api.CodeRefused, err.Error()}
		}
		return badSpec("%s", err.Error())
	}
	if sp.Network != nil && api.NetworkRank(sp.Network.Mode) < 0 {
		return badSpec("network.mode %q: none, allowlist or open", sp.Network.Mode)
	}
	if sp.Placement.Spread != "" && sp.Placement.Spread != api.SpreadNode {
		return badSpec("placement.spread %q: the one spread is %q", sp.Placement.Spread, api.SpreadNode)
	}
	if sp.Public {
		if sp.Port == 0 {
			return badSpec("a public service needs the port it listens on")
		}
		if _, ok := routerLabel(sp.Name, tenant); !ok {
			return badSpec("tenant %q cannot be named in a DNS label (lowercase letters, digits and dashes, at most 63 with the service's name), so its services cannot be public", tenant)
		}
	}
	if len(sp.Secrets) > 0 {
		return &specError{http.StatusNotImplemented, api.CodeUnsupported, errSecretsUnsupported.Error()}
	}
	return nil
}

// shape is the part of a spec that decides what a replica's sandbox is.
// Changing it is a new revision, rolled out; the rest — the count, whether
// it is public, how it is spread — applies to the replicas there are.
func shape(sp api.ServiceSpec) api.ServiceSpec {
	sp.Name, sp.Replicas, sp.Public, sp.Placement = "", 0, false, api.ServicePlacement{}
	if len(sp.Env) == 0 {
		sp.Env = nil
	}
	if len(sp.Command) == 0 {
		sp.Command = nil
	}
	return sp
}

// --- the services the gateway holds -----------------------------------------------

// serviceCtl holds every service in memory: the store's record, worked on by
// the controller and the API under the service's lock, and what the store
// does not keep — each replica's health.
type serviceCtl struct {
	mu   sync.Mutex
	svcs map[string]*service // by tenant\x00name
	kick chan struct{}

	retireBusy sync.Mutex
}

type service struct {
	mu     sync.Mutex
	rec    serviceRecord
	health map[string]*replicaHealth // by sandbox id
	gone   bool                      // deleted; nothing more is created for it

	busy sync.Mutex // one reconcile pass at a time

	// pending are the restart counts of failed replicas, handed to the
	// replicas that replace them.
	pending    []int
	crashes    int       // failures since a replica was last found healthy
	nextCreate time.Time // backoff after failures
	lastErr    string
	rr         uint64 // the router's round robin
}

func svcKey(tenant, name string) string { return tenant + "\x00" + name }

func newServiceCtl(st *FileStore) *serviceCtl {
	c := &serviceCtl{svcs: map[string]*service{}, kick: make(chan struct{}, 1)}
	for _, r := range st.Services() {
		c.svcs[svcKey(r.Tenant, r.Name)] = &service{rec: r, health: map[string]*replicaHealth{}}
	}
	return c
}

func (c *serviceCtl) get(tenant, name string) *service {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.svcs[svcKey(tenant, name)]
}

func (c *serviceCtl) list() []*service {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*service, 0, len(c.svcs))
	for _, s := range c.svcs {
		out = append(out, s)
	}
	return out
}

// poke wakes the controller without waiting for its next tick.
func (c *serviceCtl) poke() {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// --- handlers -----------------------------------------------------------------------

// tenantOf is the tenant a request names a service in: the caller's own, or
// for an admin the one ?tenant= names.
func tenantOf(r *http.Request, p Principal) string {
	if t, ok := r.URL.Query()["tenant"]; ok && p.Can(ScopeAdmin) && len(t) > 0 {
		return t[0]
	}
	return p.Tenant
}

// lookupService finds the service a request names that the caller may act
// on. Someone else's is not found, as a sandbox is.
func (g *Gateway) lookupService(w http.ResponseWriter, r *http.Request, p Principal) *service {
	s := g.services.get(tenantOf(r, p), r.PathValue("name"))
	if s != nil {
		s.mu.Lock()
		o := Owner{User: s.rec.User, Tenant: s.rec.Tenant}
		s.mu.Unlock()
		if mayAct(p, o) {
			return s
		}
	}
	writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such service")
	return nil
}

func (g *Gateway) createService(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var sp api.ServiceSpec
	if !decode(w, r, &sp) {
		return
	}
	if e := checkServiceSpec(&sp, p.Tenant); e != nil {
		writeErr(w, e.status, e.code, e.msg)
		return
	}
	now := time.Now().UTC()
	s := &service{health: map[string]*replicaHealth{}, rec: serviceRecord{
		Name: sp.Name, User: p.User, Tenant: p.Tenant, Spec: sp, Revision: 1,
		Serving: revision{Rev: 1, Spec: sp}, Members: []replicaRecord{}, Created: now, Updated: now,
	}}
	ctl := g.services
	ctl.mu.Lock()
	key := svcKey(p.Tenant, sp.Name)
	if ctl.svcs[key] != nil {
		ctl.mu.Unlock()
		writeErr(w, http.StatusConflict, api.CodeConflict, "a service named "+sp.Name+" already exists in this tenant")
		return
	}
	if err := g.store.PutService(s.rec); err != nil {
		ctl.mu.Unlock()
		g.logf("recording service %s: %v", sp.Name, err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the service could not be recorded")
		return
	}
	ctl.svcs[key] = s
	ctl.mu.Unlock()
	ctl.poke()
	writeJSON(w, http.StatusCreated, g.serviceView(s))
}

func (g *Gateway) listServices(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	out := []api.Service{}
	for _, s := range g.services.list() {
		s.mu.Lock()
		o := Owner{User: s.rec.User, Tenant: s.rec.Tenant}
		s.mu.Unlock()
		if mayAct(p, o) {
			out = append(out, g.serviceView(s))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].Spec.Name < out[j].Spec.Name
	})
	writeJSON(w, http.StatusOK, api.ServiceList{Services: out})
}

func (g *Gateway) getService(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	if s := g.lookupService(w, r, p); s != nil {
		writeJSON(w, http.StatusOK, g.serviceView(s))
	}
}

// updateService takes a new spec. A change to what a replica is starts a
// rollout to a new revision; a change to the count, to public or to the
// spread applies at once.
func (g *Gateway) updateService(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var sp api.ServiceSpec
	if !decode(w, r, &sp) {
		return
	}
	name := r.PathValue("name")
	if sp.Name == "" {
		sp.Name = name
	}
	if sp.Name != name {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "name: the spec names "+sp.Name+", the path "+name+"; a service is not renamed")
		return
	}
	s := g.lookupService(w, r, p)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such service")
		return
	}
	if e := checkServiceSpec(&sp, s.rec.Tenant); e != nil {
		writeErr(w, e.status, e.code, e.msg)
		return
	}
	newest := s.rec.Serving
	if s.rec.Target != nil {
		newest = *s.rec.Target
	}
	rec := s.rec.clone()
	if !reflect.DeepEqual(shape(sp), shape(newest.Spec)) {
		rec.Revision++
		if rec.Target != nil {
			rec.Older = append(rec.Older, *rec.Target) // superseded mid-rollout
		}
		rec.Target = &revision{Rev: rec.Revision, Spec: sp}
		rec.Rollout = &api.ServiceRollout{State: api.RolloutInProgress, From: rec.Serving.Rev, To: rec.Revision}
	}
	rec.Spec = sp
	rec.Updated = time.Now().UTC()
	if err := g.store.PutService(rec); err != nil {
		g.logf("recording service %s: %v", sp.Name, err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the service could not be recorded")
		return
	}
	s.rec = rec
	g.services.poke()
	writeJSON(w, http.StatusOK, g.serviceViewLocked(s))
}

func (g *Gateway) scaleService(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var req api.ScaleServiceRequest
	if !decode(w, r, &req) {
		return
	}
	if req.Replicas < 0 || req.Replicas > MaxServiceReplicas {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, fmt.Sprintf("replicas: 0 to %d", MaxServiceReplicas))
		return
	}
	s := g.lookupService(w, r, p)
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such service")
		return
	}
	rec := s.rec.clone()
	rec.Spec.Replicas = req.Replicas
	rec.Updated = time.Now().UTC()
	if err := g.store.PutService(rec); err != nil {
		g.logf("recording service %s: %v", rec.Name, err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the service could not be recorded")
		return
	}
	s.rec = rec
	g.services.poke()
	writeJSON(w, http.StatusOK, g.serviceViewLocked(s))
}

// deleteService forgets the service and terminates its replicas. They are
// queued for termination in the same write that removes the service, so a
// replica whose node is down now is terminated when it answers again.
func (g *Gateway) deleteService(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) || !need(w, p, ScopeDelete) {
		return
	}
	s := g.lookupService(w, r, p)
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.gone {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such service")
		return
	}
	var ids []string
	for _, m := range s.rec.Members {
		ids = append(ids, m.Sandbox)
	}
	if err := g.store.DeleteService(s.rec.Tenant, s.rec.Name, ids); err != nil {
		s.mu.Unlock()
		g.logf("deleting service %s: %v", s.rec.Name, err)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the service could not be deleted")
		return
	}
	s.gone = true
	s.rec.Members = nil
	tenant, name := s.rec.Tenant, s.rec.Name
	s.mu.Unlock()
	g.services.mu.Lock()
	delete(g.services.svcs, svcKey(tenant, name))
	g.services.mu.Unlock()
	g.terminateReplicas(r.Context(), ids)
	w.WriteHeader(http.StatusNoContent)
}

// --- the view -------------------------------------------------------------------

func (g *Gateway) serviceView(s *service) api.Service {
	s.mu.Lock()
	defer s.mu.Unlock()
	return g.serviceViewLocked(s)
}

func (g *Gateway) serviceViewLocked(s *service) api.Service {
	rec := s.rec.clone()
	out := api.Service{Spec: rec.Spec, Owner: rec.User, Tenant: rec.Tenant, Revision: rec.Revision,
		Serving: rec.Serving.Rev, Desired: rec.Spec.Replicas, Restarts: rec.Restarts, Rollout: rec.Rollout,
		Error: s.lastErr, Replicas: []api.ServiceReplica{}, CreatedAt: rec.Created, UpdatedAt: rec.Updated}
	for k := range out.Spec.Env {
		out.EnvNames = append(out.EnvNames, k)
	}
	sort.Strings(out.EnvNames)
	out.Spec.Env = nil
	if rec.Spec.Public {
		out.URL = g.routerURL(rec.Name, rec.Tenant)
	}
	for _, m := range rec.Members {
		rp := api.ServiceReplica{Sandbox: m.Sandbox, Node: m.Node, Revision: m.Rev, Restarts: m.Restarts,
			CreatedAt: m.Created, State: api.ReplicaStarting}
		if h := s.health[m.Sandbox]; h != nil {
			rp.Healthy, rp.LastError = h.healthy, h.lastErr
			if !h.lastCheck.IsZero() {
				at := h.lastCheck.UTC()
				rp.LastCheck = &at
			}
			switch {
			case h.lost:
				rp.State, rp.Healthy = api.ReplicaLost, false
			case h.healthy:
				rp.State = api.ReplicaHealthy
			case h.checked:
				rp.State = api.ReplicaUnhealthy
			}
		}
		if rp.Healthy {
			out.Ready++
		}
		out.Replicas = append(out.Replicas, rp)
	}
	return out
}
