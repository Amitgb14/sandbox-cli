package gateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Config configures a Gateway.
type Config struct {
	Store *FileStore
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
	Logf        func(format string, args ...any)
	// NewNodeClient builds a node's client; default NewNodeClient.
	NewNodeClient func(NodeConfig) (*api.Client, error)
	// SecretsKey seals secrets and jobs' environments (LoadSecretsKey).
	// Without one the secret endpoints answer unsupported.
	SecretsKey []byte
	// JobsDir holds what jobs' runs kept (default: jobs/ beside the state
	// file), and JobRetention is how long a finished job is kept (24h).
	JobsDir      string
	JobRetention time.Duration
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
	store *FileStore
	nodes *nodePool
	tombs *tombstones
	logf  func(string, ...any)

	sshMu sync.RWMutex
	ssh   SSHInfoer

	schedMu sync.Mutex // placement: candidates read, node chosen, room reserved

	quotaMu  sync.Mutex
	inflight map[string]Usage // by tenant: creates past the quota check, not yet recorded

	claimMu sync.Mutex
	claimed map[string]bool // sandbox and volume names being created

	sealer *sealer // nil without a secrets key
	jobs   *jobManager

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
	if cfg.JobsDir == "" {
		cfg.JobsDir = filepath.Join(filepath.Dir(cfg.Store.path), "jobs")
	}
	if cfg.JobRetention <= 0 {
		cfg.JobRetention = 24 * time.Hour
	}
	var seal *sealer
	if cfg.SecretsKey != nil {
		var err error
		if seal, err = newSealer(cfg.SecretsKey); err != nil {
			return nil, err
		}
	}
	g := &Gateway{sealer: seal, jobs: newJobManager(),
		cfg: cfg, store: cfg.Store, logf: cfg.Logf, tombs: newTombstones(10000, time.Hour),
		inflight: map[string]Usage{}, claimed: map[string]bool{},
		nodes: newNodePool(poolConfig{interval: cfg.PollInterval, failAfter: cfg.FailAfter,
			newClient: cfg.NewNodeClient, logf: cfg.Logf}),
	}
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
	for _, n := range cfg.Store.Nodes() {
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
	g.nodes.pollAll(ctx)
	g.wg.Add(2)
	go func() { defer g.wg.Done(); g.nodes.run(ctx) }()
	g.startJobs(ctx)
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
// within the user's tenant, and an admin's to act on whatever its owner.
func mayAct(p Principal, o Owner) bool {
	if p.Can(ScopeAdmin) {
		return true
	}
	return p.User != "" && o.User == p.User && o.Tenant == p.Tenant
}

// ownerOf is the store's record, or a tombstone's for a sandbox deleted
// through the gateway a short while ago.
func (g *Gateway) ownerOf(id string) (Owner, bool) {
	if o, ok := g.store.OwnerOf(id); ok {
		return o, true
	}
	return g.tombs.get(id)
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
		if o, ok = g.ownerOf(ref); !ok || !mayAct(p, o) {
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
		if o, ok = g.ownerOf(id); !ok || !mayAct(p, o) {
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
	done   bool
}

// reserveQuota checks a create against its tenant's quota and holds its
// share. Usage is what the store records plus what is in flight, so a burst
// of creates cannot each pass the check and together exceed it.
func (g *Gateway) reserveQuota(tenant string, r api.NodeResources) (*quotaHold, error) {
	q := g.cfg.Quota
	g.quotaMu.Lock()
	defer g.quotaMu.Unlock()
	in := g.inflight[tenant]
	if q.Sandboxes > 0 || q.CPUs > 0 || q.MemoryMB > 0 {
		u := g.store.UsageOf(tenant)
		who := "tenant " + tenant
		if tenant == "" {
			who = "the default tenant"
		}
		switch {
		case q.Sandboxes > 0 && u.Sandboxes+in.Sandboxes+1 > q.Sandboxes:
			return nil, &errQuota{fmt.Sprintf("%s holds its quota of %d sandboxes", who, q.Sandboxes)}
		case q.CPUs > 0 && u.CPUs+in.CPUs+r.CPUs > q.CPUs+1e-9:
			return nil, &errQuota{fmt.Sprintf("%s would exceed its quota of %v CPUs", who, q.CPUs)}
		case q.MemoryMB > 0 && u.MemoryMB+in.MemoryMB+r.MemoryMB > q.MemoryMB:
			return nil, &errQuota{fmt.Sprintf("%s would exceed its quota of %d MB of memory", who, q.MemoryMB)}
		}
	}
	in.Sandboxes++
	in.CPUs += r.CPUs
	in.MemoryMB += r.MemoryMB
	g.inflight[tenant] = in
	return &quotaHold{g: g, tenant: tenant, r: r}, nil
}

// drop removes the hold from the in-flight count; under quotaMu.
func (h *quotaHold) drop() {
	if h.done {
		return
	}
	h.done = true
	in := h.g.inflight[h.tenant]
	in.Sandboxes--
	in.CPUs -= h.r.CPUs
	in.MemoryMB -= h.r.MemoryMB
	if in.Sandboxes <= 0 {
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

// record makes the sandbox's owner record, and moves its share from in
// flight to recorded in one step under the quota lock, so no check counts it
// twice or not at all; the file is written after the lock is let go.
func (h *quotaHold) record(id string, o Owner, cpus float64, memoryMB int) error {
	h.g.quotaMu.Lock()
	err := h.g.store.stageSandbox(id, o, cpus, memoryMB)
	if err == nil {
		h.drop()
	}
	h.g.quotaMu.Unlock()
	if err != nil {
		return err
	}
	if err := h.g.store.persist(); err != nil {
		_ = h.g.store.ForgetSandbox(id)
		return err
	}
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
// tenant's quota.
func (g *Gateway) reconcile(ctx context.Context) {
	for _, n := range g.nodes.healthy() {
		recorded := g.store.SandboxesOn(n.cfg.Name)
		if len(recorded) == 0 {
			continue
		}
		asked := time.Now()
		var list api.SandboxList
		if err := n.getJSON(ctx, "/v1/sandboxes", nil, &list); err != nil {
			continue
		}
		state := map[string]string{}
		for _, sb := range list.Sandboxes {
			state[sb.ID] = sb.State
		}
		var forget []string
		for id, at := range recorded {
			st, listed := state[id]
			if listed && st == api.StateTerminated {
				if o, ok := g.store.OwnerOf(id); ok {
					g.tombs.add(id, o)
				}
				forget = append(forget, id)
			} else if !listed && at.Before(asked) {
				forget = append(forget, id)
			}
		}
		if err := g.store.ForgetSandboxes(forget); err != nil {
			g.logf("reconcile %s: %v", n.cfg.Name, err)
		}
	}
}
