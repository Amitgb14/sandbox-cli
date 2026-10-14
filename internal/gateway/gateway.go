package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Config configures a Gateway.
type Config struct {
	Store Store
	// StaticNodes come from the node config file, beside the store's own.
	// A name in both is refused at start.
	StaticNodes []NodeConfig
	// PollInterval is how often each node is asked for its status (default
	// 5s); a node failing FailAfter polls in a row (default 3) is unhealthy.
	PollInterval time.Duration
	FailAfter    int
	// ReconcileInterval is how often the store's owner records are checked
	// against the nodes' listings, to forget sandboxes that ended without a
	// DELETE through the gateway — an idle timeout, a node restart (default 1m).
	ReconcileInterval time.Duration
	Quota             Quota
	// Defaults is what a create that names no resources is counted as, for
	// placement and quota, until the node says what it gave. Zero fields take
	// sandboxd's built-in defaults.
	Defaults api.NodeResources
	// NodeFilesDir is the only directory whose files a node added through
	// the admin API may name (token, CA, client certificate). Empty refuses
	// file references from the API. The CLI, run by the operator on the
	// gateway's host, is not limited.
	NodeFilesDir string
	// SSHAccessTTL is the default lifetime of an SSH access token (15m), and
	// SSHAccessMaxTTL the longest one may ask for (24h).
	SSHAccessTTL    time.Duration
	SSHAccessMaxTTL time.Duration
	// CORSOrigins are browser origins allowed to call the gateway. A request
	// with any other Origin is refused, as by sandboxd.
	CORSOrigins []string
	// NodeLostAfter is how long a node may not answer before its sandboxes
	// are reported lost and stop counting against quotas (lost.go; default
	// 5m).
	NodeLostAfter time.Duration
	// Audit records every authenticated request (audit.go); nil keeps none.
	Audit *AuditLog
	// ServiceInterval is how often the service controller looks at every
	// service (default 1s); Router is where the HTTP router serves them.
	ServiceInterval time.Duration
	Router          RouterConfig
	Logf            func(format string, args ...any)
	// NewNodeClient builds a node's client; default NewNodeClient.
	NewNodeClient func(NodeConfig) (*api.Client, error)
	// SecretsKey seals secrets and jobs' environments (LoadSecretsKey).
	// Without one the secret endpoints answer unsupported.
	SecretsKey []byte
	// JobsDir holds what jobs' runs kept (default: jobs/ beside the state
	// file), and JobRetention is how long a finished job is kept (24h).
	JobsDir      string
	JobRetention time.Duration
	// SandboxHistory is how long a terminated sandbox's row is kept, as
	// history; 0 keeps them.
	SandboxHistory time.Duration
	// NotifyAllowPrivate lets a job's notify URL reach loopback, private and
	// link-local addresses; without it the gateway posts to public ones only
	// (jobrun.go, newNotifyClient).
	NotifyAllowPrivate bool
	// Mail sends invites by email (mail.go); nil sends none.
	Mail *MailConfig
	// AccessRecheckInterval is how often running jobs are held to their
	// owner still holding an active API key (default 30s;
	// revokeOrphanedJobs), and open forwarded requests to their key
	// (recheckLive). A revocation through the admin API is acted on at
	// once; this is the backstop for a store changed some other way.
	AccessRecheckInterval time.Duration
	// MaxOrgsPerUser is how many organisations one user may make or own
	// (orgs.go). Each is a tenant with a quota of its own, so this bounds
	// how far one user can multiply theirs. Zero is unlimited.
	MaxOrgsPerUser int
}

// Quota bounds what one tenant may hold at once. Zero is unlimited.
type Quota struct {
	Sandboxes int
	CPUs      float64
	MemoryMB  int
}

// SSHInfoer is what the HTTP front needs of the SSH server.
type SSHInfoer interface{ Info() api.SSHInfo }

// Gateway is sandbox-gateway's core: the node pool, the scheduler, the
// router, and the HTTP front that speaks Sandbox API v1.
type Gateway struct {
	cfg   Config
	store Store
	nodes *nodePool
	tombs *tombstones
	logf  func(string, ...any)

	metrics *gatewayMetrics // metrics.go
	audit   *AuditLog       // audit.go
	ctxMu   sync.Mutex
	ctx     context.Context // Start's, for work begun outside a request
	closing bool

	sshMu sync.RWMutex
	ssh   SSHInfoer

	schedMu sync.Mutex // placement: candidates read, node chosen, room reserved

	quotaMu  sync.Mutex
	inflight map[string]Usage // by tenant: creates past the quota check, not yet recorded

	claimMu sync.Mutex
	claimed map[string]bool // sandbox and volume names being created

	sealer       *sealer // nil without a secrets key
	jobs         *jobManager
	notifyClient *http.Client
	services     *serviceCtl
	live         *liveRequests   // open forwarded requests, held to their keys (live.go)
	signIns      *signInThrottle // accounts.go
	active       activeCache     // userActiveCached

	stop context.CancelFunc
	wg   sync.WaitGroup
}

// New builds a gateway over cfg.Store and the nodes it and cfg name. Call
// Start to begin polling them.
func New(cfg Config) (*Gateway, error) {
	if cfg.Store == nil {
		return nil, errors.New("a gateway needs a store")
	}
	def := spec.DefaultPolicy()
	if cfg.Defaults.CPUs == 0 {
		cfg.Defaults.CPUs = def.DefaultCPUs
	}
	if cfg.Defaults.MemoryMB == 0 {
		cfg.Defaults.MemoryMB = def.DefaultMemoryMB
	}
	if cfg.Defaults.DiskMB == 0 {
		cfg.Defaults.DiskMB = def.DefaultDiskMB
	}
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = time.Minute
	}
	if cfg.SSHAccessTTL <= 0 {
		cfg.SSHAccessTTL = 15 * time.Minute
	}
	if cfg.SSHAccessMaxTTL <= 0 {
		cfg.SSHAccessMaxTTL = 24 * time.Hour
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.NodeLostAfter <= 0 {
		cfg.NodeLostAfter = 5 * time.Minute
	}
	if cfg.JobsDir == "" {
		return nil, errors.New("a gateway needs a jobs directory (Config.JobsDir)")
	}
	if cfg.JobRetention <= 0 {
		cfg.JobRetention = 24 * time.Hour
	}
	if cfg.AccessRecheckInterval <= 0 {
		cfg.AccessRecheckInterval = 30 * time.Second
	}
	var seal *sealer
	if cfg.SecretsKey != nil {
		var err error
		if seal, err = newSealer(cfg.SecretsKey); err != nil {
			return nil, err
		}
	}
	services, err := cfg.Store.Services(context.Background())
	if err != nil {
		return nil, err
	}
	g := &Gateway{sealer: seal, jobs: newJobManager(), notifyClient: newNotifyClient(cfg.NotifyAllowPrivate),
		cfg: cfg, store: cfg.Store, logf: cfg.Logf, tombs: newTombstones(10000, time.Hour),
		inflight: map[string]Usage{}, claimed: map[string]bool{},
		metrics: newGatewayMetrics(), audit: cfg.Audit, services: newServiceCtl(services),
		live: newLiveRequests(), signIns: newSignInThrottle(time.Now),
	}
	g.nodes = newNodePool(poolConfig{interval: cfg.PollInterval, failAfter: cfg.FailAfter,
		newClient: cfg.NewNodeClient, logf: cfg.Logf, changed: g.nodeChanged, polled: g.nodePolled})
	seen := map[string]bool{}
	for _, n := range cfg.StaticNodes {
		if seen[n.Name] {
			return nil, fmt.Errorf("node %s is in the node config file twice", n.Name)
		}
		seen[n.Name] = true
		if _, err := g.nodes.add(n, true); err != nil {
			return nil, err
		}
	}
	stored, err := cfg.Store.Nodes(context.Background())
	if err != nil {
		return nil, err
	}
	for _, n := range stored {
		if seen[n.Name] {
			return nil, fmt.Errorf("node %s is both in the node config file and in the state; remove one", n.Name)
		}
		if _, err := g.nodes.add(n, false); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Start polls every node once, so a gateway that has just started knows
// which nodes are up before its first create, and then keeps polling and
// reconciling until Close.
func (g *Gateway) Start(ctx context.Context) {
	ctx, g.stop = context.WithCancel(ctx)
	g.ctxMu.Lock()
	g.ctx = ctx
	g.ctxMu.Unlock()
	g.nodes.pollAll(ctx)
	g.wg.Add(5)
	go func() { defer g.wg.Done(); g.nodes.run(ctx) }()
	go func() { defer g.wg.Done(); g.pruneHistory(ctx) }()
	g.startJobs(ctx)
	// A key revoked while the gateway was down (the CLI's keys revoke) is
	// acted on now, before a resumed job makes another sandbox.
	g.revokeOrphanedJobs()
	go func() { defer g.wg.Done(); g.runServices(ctx) }()
	go func() {
		defer g.wg.Done()
		t := time.NewTicker(g.cfg.AccessRecheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.revokeOrphanedJobs()
				g.recheckLive()
			}
		}
	}()
	go func() {
		defer g.wg.Done()
		t := time.NewTicker(g.cfg.ReconcileInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.reconcile(ctx)
			}
		}
	}()
}

// Close stops polling. It does not close the store.
func (g *Gateway) Close() {
	g.ctxMu.Lock()
	g.closing = true
	g.ctxMu.Unlock()
	if g.stop != nil {
		g.stop()
	}
	g.jobs.close()
	g.wg.Wait()
}

// Nodes reports every node as the gateway sees it, by name.
func (g *Gateway) Nodes() []api.NodeInfo {
	var out []api.NodeInfo
	for _, n := range g.nodes.list() {
		out = append(out, n.info())
	}
	return out
}

// PollNow polls every node at once.
func (g *Gateway) PollNow(ctx context.Context) { g.nodes.pollAll(ctx) }

// SetSSH attaches the SSH server, whose Info GET /v1/ssh serves. Without
// one, the SSH endpoints answer that SSH is off.
func (g *Gateway) SetSSH(s SSHInfoer) {
	g.sshMu.Lock()
	g.ssh = s
	g.sshMu.Unlock()
}

// accessRechecker is the SSH server's RecheckAccess.
type accessRechecker interface{ RecheckAccess() int }

// accessChanged is called once a key has been revoked or an SSH key
// removed, before the request that did it is answered. Revocation takes
// effect on what is already running, not only on what starts next: open
// API requests whose key no longer allows them are ended (live.go), open SSH
// connections that no longer pass the login check are closed, and running
// jobs whose owner holds no active key are cancelled. A service needs
// nothing here: it is routed to and given replicas only while its owner is
// active (pickReplica, ownerActive), which both check every time.
func (g *Gateway) accessChanged() {
	g.active.clear()
	g.recheckLive()
	g.revokeOrphanedJobs()
	g.sshMu.RLock()
	s := g.ssh
	g.sshMu.RUnlock()
	if r, ok := s.(accessRechecker); ok {
		r.RecheckAccess()
	}
}

func (g *Gateway) sshInfo() (api.SSHInfo, bool) {
	g.sshMu.RLock()
	defer g.sshMu.RUnlock()
	if g.ssh == nil {
		return api.SSHInfo{}, false
	}
	return g.ssh.Info(), true
}

// --- the router ---------------------------------------------------------------

// mayAct is the ownership rule: a sandbox, volume or snapshot is its user's,
// within the user's tenant, and nobody else's — an admin's included. The
// super admin runs the gateway (users, nodes, images, audit) and sees its
// totals (GET /v1/admin/stats), not what is in other users' sandboxes; to
// stop a user's work they suspend the user.
func mayAct(p Principal, o Owner) bool {
	return p.User != "" && o.User == p.User && o.Tenant == p.Tenant
}

// mayEnd is the one thing an admin may do to a sandbox not theirs:
// terminate it, by its id (DELETE, the only route resolved with
// sandbox:delete). It is enforcement, not access: nothing of what is in the
// sandbox is shown, it is found by id only, never by name or in a listing,
// and it is how one left running by a user removed from an organisation, or
// suspended, is ended.
func mayEnd(p Principal, scope string) bool {
	return scope == ScopeDelete && p.Can(ScopeAdmin)
}

// ownerOf is the store's record, or a tombstone's for a sandbox deleted
// through the gateway a short while ago.
func (g *Gateway) ownerOf(ctx context.Context, p Principal, id string) (Owner, bool, error) {
	o, ok, err := g.storeFor(p).OwnerOf(ctx, id)
	if err != nil || ok {
		return o, ok, err
	}
	o, ok = g.tombs.get(id)
	return o, ok, nil
}

// Resolve implements Router.
func (g *Gateway) Resolve(ctx context.Context, p Principal, ref, scope string) (string, *api.Client, error) {
	id, _, n, err := g.resolve(ctx, p, ref, scope)
	if err != nil {
		return "", nil, err
	}
	return id, n.client, nil
}

// resolve is Resolve with the owner and the node. The order matters: the
// scope is checked before the sandbox is looked up, so a key without it
// learns nothing about which sandboxes exist; and a sandbox the caller may
// not act on is not found, never forbidden, so its id cannot be probed.
func (g *Gateway) resolve(ctx context.Context, p Principal, ref, scope string) (string, Owner, *node, error) {
	if p.User == "" {
		return "", Owner{}, nil, ErrUnauthenticated
	}
	if scope == "" || !p.Can(scope) {
		return "", Owner{}, nil, ErrForbidden
	}
	var id string
	var o Owner
	switch {
	case spec.ValidID(ref):
		var ok bool
		var err error
		if o, ok, err = g.ownerOf(ctx, p, ref); err != nil {
			return "", Owner{}, nil, err
		} else if !ok || !(mayAct(p, o) || mayEnd(p, scope)) {
			return "", Owner{}, nil, ErrNotFound
		}
		id = ref
	case spec.ValidName(ref):
		// A name is looked up among the caller's own sandboxes only, so two
		// users may each have a "web".
		sb, err := g.byName(ctx, p, ref)
		if err != nil {
			return "", Owner{}, nil, err
		}
		id = sb
		var ok bool
		if o, ok, err = g.ownerOf(ctx, p, id); err != nil {
			return "", Owner{}, nil, err
		} else if !ok || !mayAct(p, o) {
			return "", Owner{}, nil, ErrNotFound
		}
	default:
		return "", Owner{}, nil, ErrNotFound
	}
	if p.Sandbox != "" && p.Sandbox != id {
		return "", Owner{}, nil, ErrNotFound
	}
	// The id may name a node; the store, which recorded where the gateway
	// sent the create, decides. Disagreement means one of them is wrong, and
	// the request goes nowhere.
	if named, ok := api.NodeOfID(id); ok && named != o.Node {
		g.logf("sandbox %s names node %s but is recorded on %s; refusing to route it", id, named, o.Node)
		return "", Owner{}, nil, ErrNotFound
	}
	noteSandbox(ctx, id, o.Node)
	n := g.nodes.get(o.Node)
	if n == nil || !n.isHealthy() {
		return "", Owner{}, nil, ErrNodeDown
	}
	return id, o, n, nil
}

// byName finds the caller's sandbox called name: the live one, or else the
// newest terminated one (so deleting by name twice stays idempotent), as a
// node does.
func (g *Gateway) byName(ctx context.Context, p Principal, name string) (string, error) {
	list, err := g.listSandboxes(ctx, p, nil, false)
	if err != nil {
		return "", err
	}
	var best *api.Sandbox
	for i := range list {
		sb := &list[i]
		if sb.Name != name {
			continue
		}
		if sb.State != api.StateTerminated {
			return sb.ID, nil
		}
		if best == nil || sb.CreatedAt.After(best.CreatedAt) {
			best = sb
		}
	}
	if best == nil {
		return "", ErrNotFound
	}
	return best.ID, nil
}

var _ Router = (*Gateway)(nil)

// --- claims, quota, tombstones ------------------------------------------------

// claim marks a name as being created, so two concurrent creates of one name
// cannot both pass a check-then-create.
func (g *Gateway) claim(key string) bool {
	g.claimMu.Lock()
	defer g.claimMu.Unlock()
	if g.claimed[key] {
		return false
	}
	g.claimed[key] = true
	return true
}

func (g *Gateway) unclaim(key string) {
	g.claimMu.Lock()
	delete(g.claimed, key)
	g.claimMu.Unlock()
}

// errQuota is a create over its tenant's quota.
type errQuota struct{ msg string }

func (e *errQuota) Error() string { return e.msg }

// quotaHold is a create's share of its tenant's quota, held from the check
// until the sandbox is recorded (record) or the create fails (release).
type quotaHold struct {
	g      *Gateway
	tenant string
	r      api.NodeResources
	n      int // sandboxes: 1 for a create, 0 for a resize's growth
	done   bool
}

// reserveQuota checks a create against its tenant's quota and holds its
// share. Usage is what the store records plus what is in flight, so a burst
// of creates cannot each pass the check and together exceed it.
func (g *Gateway) reserveQuota(ctx context.Context, tenant string, r api.NodeResources) (*quotaHold, error) {
	return g.reserve(ctx, tenant, r, 1)
}

// reserveGrowth checks what a resize adds to a sandbox (resize.go) against
// its tenant's quota, as a create's share is, without counting a sandbox.
func (g *Gateway) reserveGrowth(ctx context.Context, tenant string, r api.NodeResources) (*quotaHold, error) {
	return g.reserve(ctx, tenant, r, 0)
}

func (g *Gateway) reserve(ctx context.Context, tenant string, r api.NodeResources, n int) (*quotaHold, error) {
	q := g.cfg.Quota
	g.quotaMu.Lock()
	defer g.quotaMu.Unlock()
	in := g.inflight[tenant]
	if q.Sandboxes > 0 || q.CPUs > 0 || q.MemoryMB > 0 {
		u, err := g.usageOf(ctx, tenant) // less what is on lost nodes
		if err != nil {
			return nil, err
		}
		who := "tenant " + tenant
		if tenant == "" {
			who = "the default tenant"
		}
		switch {
		case n > 0 && q.Sandboxes > 0 && u.Sandboxes+in.Sandboxes+n > q.Sandboxes:
			return nil, &errQuota{fmt.Sprintf("%s holds its quota of %d sandboxes", who, q.Sandboxes)}
		case q.CPUs > 0 && u.CPUs+in.CPUs+r.CPUs > q.CPUs+1e-9:
			return nil, &errQuota{fmt.Sprintf("%s would exceed its quota of %v CPUs", who, q.CPUs)}
		case q.MemoryMB > 0 && u.MemoryMB+in.MemoryMB+r.MemoryMB > q.MemoryMB:
			return nil, &errQuota{fmt.Sprintf("%s would exceed its quota of %d MB of memory", who, q.MemoryMB)}
		}
	}
	in.Sandboxes += n
	in.CPUs += r.CPUs
	in.MemoryMB += r.MemoryMB
	g.inflight[tenant] = in
	return &quotaHold{g: g, tenant: tenant, r: r, n: n}, nil
}

// drop removes the hold from the in-flight count; under quotaMu.
func (h *quotaHold) drop() {
	if h.done {
		return
	}
	h.done = true
	in := h.g.inflight[h.tenant]
	in.Sandboxes -= h.n
	in.CPUs -= h.r.CPUs
	in.MemoryMB -= h.r.MemoryMB
	if in.Sandboxes <= 0 && in.CPUs < 1e-9 && in.MemoryMB <= 0 {
		delete(h.g.inflight, h.tenant)
	} else {
		h.g.inflight[h.tenant] = in
	}
}

// release gives the share back: the create did not happen.
func (h *quotaHold) release() {
	h.g.quotaMu.Lock()
	defer h.g.quotaMu.Unlock()
	h.drop()
}

// record makes the sandbox's record — owner, size and the details its node
// answered with — and moves its share from in flight to recorded in one
// step under the quota lock, so no check counts it twice or not at all.
func (h *quotaHold) record(ctx context.Context, st Store, o Owner, sb api.Sandbox) error {
	h.g.quotaMu.Lock()
	defer h.g.quotaMu.Unlock()
	size := api.NodeResources{CPUs: sb.CPUs, MemoryMB: sb.MemoryMB, DiskMB: sb.DiskMB}
	// Recorded even if the caller has gone: the sandbox exists on the node.
	if err := st.RecordSandbox(context.WithoutCancel(ctx), sb.ID, o, size, &sb); err != nil {
		return err
	}
	h.drop()
	return nil
}

// tombstones remember, for a while, who owned a sandbox the gateway has
// forgotten: after a DELETE the node still answers for the terminated
// sandbox (its final state, its audit events, a second DELETE), and the
// caller must still be the only one who can ask. In memory only; after a
// restart a deleted sandbox is not found, which is the safe answer.
type tombstones struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	m     map[string]tomb
	order []string
}

type tomb struct {
	o  Owner
	at time.Time
}

func newTombstones(max int, ttl time.Duration) *tombstones {
	return &tombstones{max: max, ttl: ttl, m: map[string]tomb{}}
}

func (t *tombstones) add(id string, o Owner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.m[id]; !ok {
		t.order = append(t.order, id)
	}
	t.m[id] = tomb{o: o, at: time.Now()}
	for len(t.order) > t.max {
		delete(t.m, t.order[0])
		t.order = t.order[1:]
	}
}

func (t *tombstones) get(id string) (Owner, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tb, ok := t.m[id]
	if !ok || time.Since(tb.at) > t.ttl {
		return Owner{}, false
	}
	return tb.o, true
}

// reconcile forgets owner records of sandboxes their node no longer runs:
// terminated without a DELETE through the gateway (an idle timeout), or gone
// (a node that restarted). What is forgotten no longer counts against its
// tenant's quota, and is kept as history. The details of those still
// running are brought up to the node's: a lifetime moved, a suspend.
func (g *Gateway) reconcile(ctx context.Context) {
	for _, n := range g.nodes.healthy() {
		g.reconcileNode(ctx, n)
	}
}

// reconcileNode is reconcile for one node: also run when a node answers
// again after not answering (lost.go).
func (g *Gateway) reconcileNode(ctx context.Context, n *node) {
	recorded, err := g.store.SandboxesOn(ctx, n.cfg.Name)
	if err != nil {
		g.logf("reconcile %s: %v", n.cfg.Name, err)
		return
	}
	if len(recorded) == 0 {
		return
	}
	asked := time.Now()
	var list api.SandboxList
	if err := n.getJSON(ctx, "/v1/sandboxes", nil, &list); err != nil {
		return
	}
	state := map[string]string{}
	var live []api.Sandbox
	for _, sb := range list.Sandboxes {
		state[sb.ID] = sb.State
		if _, ours := recorded[sb.ID]; ours && sb.State != api.StateTerminated {
			live = append(live, sb)
		}
	}
	var forget, gone []string
	for id, at := range recorded {
		st, listed := state[id]
		if listed && st == api.StateTerminated {
			if o, ok, _ := g.store.OwnerOf(ctx, id); ok {
				g.tombs.add(id, o)
			}
			forget = append(forget, id)
		} else if !listed && at.Before(asked) {
			gone = append(gone, id)
		}
	}
	if err := g.store.UpdateSandboxDetails(ctx, live...); err != nil {
		g.logf("reconcile %s: %v", n.cfg.Name, err)
	}
	if err := g.store.ForgetSandboxes(ctx, gone, "gone"); err != nil {
		g.logf("reconcile %s: %v", n.cfg.Name, err)
		return
	}
	forget = append(forget, gone...)
	if err := g.store.ForgetSandboxes(ctx, forget, "terminated"); err != nil {
		g.logf("reconcile %s: %v", n.cfg.Name, err)
		return
	}
	g.metrics.terminated.Add(float64(len(forget)), "ended")
}

// pruneHistory deletes terminated sandboxes' rows older than
// SandboxHistory, at start and every hour.
func (g *Gateway) pruneHistory(ctx context.Context) {
	if g.cfg.SandboxHistory <= 0 {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := g.store.PruneSandboxHistory(ctx, time.Now().Add(-g.cfg.SandboxHistory)); err != nil {
			g.logf("sandbox history: %v", err)
		} else if n > 0 {
			g.logf("sandbox history: %d terminated sandboxes past %v forgotten", n, g.cfg.SandboxHistory)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// baseCtx is Start's context, ended by Close.
func (g *Gateway) baseCtx() context.Context {
	g.ctxMu.Lock()
	defer g.ctxMu.Unlock()
	if g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

// goBackground runs fn on a goroutine Close waits for, unless Close has
// begun. Add and Wait are ordered by ctxMu, as a WaitGroup requires.
func (g *Gateway) goBackground(fn func(ctx context.Context)) {
	g.ctxMu.Lock()
	if g.closing {
		g.ctxMu.Unlock()
		return
	}
	g.wg.Add(1)
	ctx := g.ctx
	g.ctxMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		defer g.wg.Done()
		fn(ctx)
	}()
}
