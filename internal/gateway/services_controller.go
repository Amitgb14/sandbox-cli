package gateway

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The controller keeps each service's count true. Every tick it looks at
// each service once: it marks replicas whose node is lost or whose sandbox is
// gone, starts the health checks that are due, and then takes one step at a
// time toward what the spec asks for — retire a failed replica, create one,
// retire an old one — until there is nothing to do. Each step is decided
// under the service's lock and done outside it, so an API call never waits
// on a node.
//
// The revision replicas are kept at is the target while a rollout is in
// progress and the serving one otherwise. Replicas of any other revision are
// the old ones: while there are any, new ones are created one at a time,
// each only once the one before is healthy, and an old one is retired only
// when a new one is healthy in its place (one surge replica, none
// unavailable). A new revision's replica failing its checks stops the
// rollout: the serving revision stays, and any of the new revision's
// replicas are replaced by it in the same way.

// serviceInterval is how often the controller looks at every service.
func (g *Gateway) serviceInterval() time.Duration {
	if g.cfg.ServiceInterval > 0 {
		return g.cfg.ServiceInterval
	}
	return time.Second
}

// runServices is the controller's loop, until ctx ends.
func (g *Gateway) runServices(ctx context.Context) {
	t := time.NewTicker(g.serviceInterval())
	defer t.Stop()
	for {
		g.serviceTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-g.services.kick:
		}
	}
}

// serviceTick starts a pass over each service not already in one, and over
// the retire queue.
func (g *Gateway) serviceTick(ctx context.Context) {
	if g.services.retireBusy.TryLock() {
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer g.services.retireBusy.Unlock()
			g.terminateReplicas(ctx, g.store.Retiring())
		}()
	}
	for _, s := range g.services.list() {
		if !s.busy.TryLock() {
			continue
		}
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			defer s.busy.Unlock()
			g.reconcileService(ctx, s)
		}()
	}
}

// reconcileService is one pass over one service.
func (g *Gateway) reconcileService(ctx context.Context, s *service) {
	g.observeService(ctx, s)
	// A pass ends when there is nothing to do; the bound keeps one service
	// from holding its pass forever if a node keeps answering oddly.
	for range 4*MaxServiceReplicas + 8 {
		if ctx.Err() != nil {
			return
		}
		act := g.decide(s, time.Now())
		if act == nil {
			return
		}
		act(ctx)
	}
}

// specOf is the spec a replica of rev was made from, and is checked by.
func (s *service) specOf(rev int) api.ServiceSpec {
	if s.rec.Target != nil && s.rec.Target.Rev == rev {
		return s.rec.Target.Spec
	}
	for _, o := range s.rec.Older {
		if o.Rev == rev {
			return o.Spec
		}
	}
	return s.rec.Serving.Spec
}

func (s *service) h(id string) *replicaHealth {
	h := s.health[id]
	if h == nil {
		h = &replicaHealth{}
		s.health[id] = h
	}
	return h
}

// observe marks what can be seen without asking a guest — a replica whose
// node is unhealthy is lost, one the store no longer records is gone — and
// starts the checks that are due.
func (g *Gateway) observeService(ctx context.Context, s *service) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return
	}
	for _, m := range s.rec.Members {
		h := s.h(m.Sandbox)
		n := g.nodes.get(m.Node)
		h.lost = n == nil || !n.isHealthy()
		if o, ok := g.store.OwnerOf(m.Sandbox); !ok || o.Node != m.Node {
			h.fatal, h.lastErr = true, "the sandbox is gone"
		}
		if h.lost || h.fatal || h.checking {
			continue
		}
		sp := s.specOf(m.Rev)
		every := 10 * time.Second
		if sp.Health != nil {
			every = time.Duration(sp.Health.EverySecs) * time.Second
		}
		if !h.lastCheck.IsZero() && now.Sub(h.lastCheck) < every {
			continue
		}
		h.checking = true
		g.wg.Add(1)
		go func() {
			defer g.wg.Done()
			res := g.check(ctx, n, m, sp)
			s.mu.Lock()
			defer s.mu.Unlock()
			h.apply(res, time.Now())
			if h.healthy {
				s.crashes = 0
			}
		}()
	}
}

// action is one step a pass takes, outside the service's lock.
type action func(ctx context.Context)

// decide looks at the service under its lock and returns the next step, or
// nil when there is none. A step that only changes the record — a rollout
// done — is made here and returns a no-op, so the pass looks again.
func (g *Gateway) decide(s *service, now time.Time) action {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return nil
	}
	rec := &s.rec
	desired := rec.Spec.Replicas
	want := rec.Serving
	if rec.Target != nil {
		want = *rec.Target
	}

	// Replicas that failed, or were lost with their node, leave the service
	// first, all at once: they are counted as serving by nothing below.
	var drop []string
	keep := rec.Members[:0:0]
	rolloutFailed := ""
	for _, m := range rec.Members {
		h := s.h(m.Sandbox)
		sp := s.specOf(m.Rev)
		failures := 3
		if sp.Health != nil {
			failures = sp.Health.Failures
		}
		failed := h.fatal || h.fails >= failures
		if !failed && !h.lost {
			keep = append(keep, m)
			continue
		}
		drop = append(drop, m.Sandbox)
		if h.lost {
			g.logf("service %s: replica %s is lost with node %s; replacing it", rec.Name, m.Sandbox, m.Node)
			s.pending = append(s.pending, m.Restarts)
			continue
		}
		g.logf("service %s: replica %s failed: %s; replacing it", rec.Name, m.Sandbox, h.lastErr)
		rec.Restarts++
		s.crashes++
		s.nextCreate = now.Add(backoff(s.crashes))
		s.pending = append(s.pending, m.Restarts+1)
		if rec.Target != nil && m.Rev == rec.Target.Rev && rolloutFailed == "" {
			rolloutFailed = "replica " + m.Sandbox + " of revision " + strconv.Itoa(m.Rev) + " failed: " + h.lastErr
		}
	}
	if len(drop) > 0 {
		rec.Members = keep
		if rolloutFailed != "" {
			g.failRollout(s, rolloutFailed)
		}
		return g.retireStep(s, drop)
	}

	var cur, old []replicaRecord
	healthyCur := 0
	starting := 0
	for _, m := range rec.Members {
		if m.Rev != want.Rev {
			old = append(old, m)
			continue
		}
		cur = append(cur, m)
		if s.h(m.Sandbox).healthy {
			healthyCur++
		} else {
			starting++
		}
	}

	// An old replica goes once a new one is healthy in its place.
	if len(old) > 0 && healthyCur+len(old) > desired {
		return g.retireStep(s, []string{pickRetire(s, old, false)})
	}
	// More of the wanted revision than asked for: scaled down.
	if len(cur) > desired {
		return g.retireStep(s, []string{pickRetire(s, cur, true)})
	}
	// A rollout is done when the new revision is all there is, and healthy.
	if rec.Target != nil && len(old) == 0 && healthyCur == desired {
		rec.Serving, rec.Target = *rec.Target, nil
		rec.Rollout.State = api.RolloutDone
		rec.Updated = now.UTC()
		g.persistService(s, nil)
		return func(context.Context) {}
	}
	if len(cur) < desired && !now.Before(s.nextCreate) && g.ownerActive(s) {
		// While old replicas serve, one new replica at a time.
		if len(old) > 0 && starting > 0 {
			return nil
		}
		return g.createStep(s, want)
	}
	return nil
}

// ownerActive refuses new replicas for a user with no API key left, as SSH
// does (userActive): revoking a user's keys stops what runs in their name
// from growing. What already runs stays until an admin deletes the service.
func (g *Gateway) ownerActive(s *service) bool {
	if userActive(g.store, s.rec.User) {
		return true
	}
	s.lastErr = "the service's owner holds no active API key; no replica is created"
	return false
}

func (g *Gateway) failRollout(s *service, reason string) {
	rec := &s.rec
	g.logf("service %s: rollout to revision %d failed: %s", rec.Name, rec.Target.Rev, reason)
	rec.Rollout = &api.ServiceRollout{State: api.RolloutFailed, From: rec.Serving.Rev, To: rec.Target.Rev, Reason: reason}
	rec.Older = append(rec.Older, *rec.Target)
	rec.Target = nil
	rec.Updated = time.Now().UTC()
	// The failure is the revision's, not the serving one's: replacing the
	// new replicas with serving ones starts at once.
	s.crashes, s.nextCreate = 0, time.Time{}
}

// pickRetire chooses which replica goes: one not healthy first, then the
// newest (scaling down) or the oldest (retiring an old revision).
func pickRetire(s *service, from []replicaRecord, newest bool) string {
	best := -1
	for i, m := range from {
		if best < 0 {
			best = i
			continue
		}
		bh, mh := s.h(from[best].Sandbox).healthy, s.h(m.Sandbox).healthy
		if bh != mh {
			if bh {
				best = i
			}
			continue
		}
		if newest == m.Created.After(from[best].Created) {
			best = i
		}
	}
	return from[best].Sandbox
}

func backoff(crashes int) time.Duration {
	if crashes <= 1 {
		return 0
	}
	d := time.Second << min(crashes-2, 6)
	return min(d, time.Minute)
}

// persistService writes the record, with replicas queued for termination;
// under s.mu. A failed write is logged: the controller carries on from what
// it holds, and the next write catches the file up.
func (g *Gateway) persistService(s *service, retire []string) {
	s.rec.pruneOlder()
	if err := g.store.PutServiceRetiring(s.rec, retire); err != nil {
		g.logf("service %s: recording: %v", s.rec.Name, err)
	}
}

// retireStep takes replicas out of the service and terminates them; under
// s.mu.
func (g *Gateway) retireStep(s *service, ids []string) action {
	rec := &s.rec
	for _, id := range ids {
		for i, m := range rec.Members {
			if m.Sandbox == id {
				rec.Members = append(rec.Members[:i:i], rec.Members[i+1:]...)
				break
			}
		}
		delete(s.health, id)
	}
	rec.Updated = time.Now().UTC()
	g.persistService(s, ids)
	return func(ctx context.Context) { g.terminateReplicas(ctx, ids) }
}

// createStep makes one replica of rev; under s.mu.
func (g *Gateway) createStep(s *service, rev revision) action {
	rec := s.rec
	var spread map[string]int
	if rec.Spec.Placement.Spread == api.SpreadNode {
		spread = map[string]int{}
		for _, m := range rec.Members {
			spread[m.Node]++
		}
	}
	restarts := 0
	if len(s.pending) > 0 {
		restarts, s.pending = s.pending[0], s.pending[1:]
	}
	p := Principal{User: rec.User, Tenant: rec.Tenant, Scopes: []string{ScopeCreate}}
	return func(ctx context.Context) {
		m, err := g.startReplica(ctx, p, rec.Name, rev, spread)
		s.mu.Lock()
		if err != nil {
			s.pending = append([]int{restarts}, s.pending...)
			g.createFailed(s, rev, err)
			s.mu.Unlock()
			return
		}
		if s.gone {
			// Deleted while it was being made.
			if err := g.store.Retire(m.Sandbox); err != nil {
				g.logf("retire queue: %v", err)
			}
			s.mu.Unlock()
			g.terminateReplicas(ctx, []string{m.Sandbox})
			return
		}
		// A revision that changed while it was being made is still known
		// (Serving, Target or Older), and the next step retires it if it
		// is not wanted.
		m.Restarts = restarts
		s.rec.Members = append(s.rec.Members, m)
		s.lastErr = ""
		s.rec.Updated = time.Now().UTC()
		g.persistService(s, nil)
		s.mu.Unlock()
	}
}

// createFailed records why a replica could not be made. In a rollout, a
// node refusing the new revision (a 4xx: a bad image, a reserved variable,
// over quota) ends the rollout; anything else — no room, a node not
// answering — is tried again after a backoff.
func (g *Gateway) createFailed(s *service, rev revision, err error) {
	g.logf("service %s: creating a replica of revision %d: %v", s.rec.Name, rev.Rev, err)
	s.lastErr = err.Error()
	s.crashes++
	s.nextCreate = time.Now().Add(max(backoff(s.crashes), time.Second))
	if s.rec.Target != nil && s.rec.Target.Rev == rev.Rev && refusedNow(err) {
		g.failRollout(s, "creating a replica of revision "+strconv.Itoa(rev.Rev)+": "+err.Error())
		g.persistService(s, nil)
	}
}

// refusedNow reports whether err says the request itself was refused, so
// trying again will not help.
func refusedNow(err error) bool {
	if ce, ok := err.(*createError); ok {
		return ce.refused()
	}
	_, ok := err.(*startError)
	return ok
}

// startError is a replica whose command could not be started.
type startError struct{ err error }

func (e *startError) Error() string { return "starting the command: " + e.err.Error() }

// startReplica creates one replica's sandbox through the gateway's own
// create path, with the service's labels, and starts its command.
func (g *Gateway) startReplica(ctx context.Context, p Principal, name string, rev revision, spread map[string]int) (replicaRecord, error) {
	sp := rev.Spec
	req := api.CreateSandboxRequest{Image: sp.Image, CPUs: sp.Resources.CPUs, MemoryMB: sp.Resources.MemoryMB,
		DiskMB: sp.Resources.DiskMB, Env: sp.Env, Network: sp.Network}
	opts := createOpts{spread: spread, labels: map[string]string{
		api.LabelService: name, api.LabelServiceRevision: strconv.Itoa(rev.Rev)}}
	c, cerr := g.createFor(ctx, p, req, opts)
	if cerr != nil {
		return replicaRecord{}, cerr
	}
	m := replicaRecord{Sandbox: c.sb.ID, Node: c.node, Rev: rev.Rev, Created: time.Now().UTC()}
	if len(sp.Command) > 0 {
		n := g.nodes.get(c.node)
		var err error
		var proc api.Process
		if n == nil {
			err = ErrNodeDown
		} else {
			proc, err = n.client.StartProcess(ctx, c.sb.ID, api.RunRequest{Argv: sp.Command})
		}
		if err != nil {
			if qerr := g.store.Retire(c.sb.ID); qerr != nil {
				g.logf("retire queue: %v", qerr)
			}
			g.terminateReplicas(context.WithoutCancel(ctx), []string{c.sb.ID})
			return replicaRecord{}, &startError{err}
		}
		m.PID = proc.PID
	}
	return m, nil
}

// terminateReplicas terminates replica sandboxes, and drops from the retire
// queue each one that is terminated or already gone. One whose node is not
// answering stays queued for the next pass.
func (g *Gateway) terminateReplicas(ctx context.Context, ids []string) {
	var done []string
	for _, id := range ids {
		o, ok := g.store.OwnerOf(id)
		if !ok {
			done = append(done, id)
			continue
		}
		n := g.nodes.get(o.Node)
		if n == nil || !n.isHealthy() {
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resp, err := n.do(dctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(id), nil, nil, "")
		cancel()
		if err != nil {
			g.logf("terminating replica %s: %v", id, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
			g.logf("terminating replica %s: node %s answered %s", id, o.Node, resp.Status)
			continue
		}
		g.tombs.add(id, o)
		if err := g.store.ForgetSandbox(id); err != nil {
			g.logf("forgetting %s: %v", id, err)
		}
		done = append(done, id)
	}
	if err := g.store.Retired(done...); err != nil {
		g.logf("retire queue: %v", err)
	}
}
