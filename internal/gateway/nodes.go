package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A node is one sandboxd the gateway reaches with the node's own token. The
// token never leaves the gateway: every forwarded request has the caller's
// credential taken off and the node's put on.

type node struct {
	cfg    NodeConfig
	static bool // from the node config file: removed by editing it, not through the API
	client *api.Client
	base   *url.URL
	rt     http.RoundTripper
	token  string

	mu       sync.Mutex
	healthy  bool
	fails    int
	lastSeen time.Time
	lastErr  string
	status   *api.NodeStatus
	placed   map[*placement]struct{}
	heldFree int // polls in a row whose Free was not taken (poll)

	// downSince is when the node last answered, or was added if never
	// (lost.go); meaningful only while it is unhealthy. Under mu.
	downSince time.Time
	// wantCordon is the cordon the gateway asked for (drain.go), put back
	// on a node that restarted without it. Under mu.
	wantCordon bool
}

// placement is a sandbox the scheduler sent to a node and the node's status
// may not show yet.
type placement struct {
	res  api.NodeResources
	sent time.Time
	done time.Time // zero while the create is in flight
}

func newNode(cfg NodeConfig, static bool, c *api.Client) *node {
	base, rt, token := c.Transport()
	return &node{cfg: cfg, static: static, client: c, base: base, rt: rt, token: token, placed: map[*placement]struct{}{},
		downSince: time.Now()}
}

// do sends one request to the node with the node's token.
func (n *node) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := *n.base
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	return n.rt.RoundTrip(req)
}

// getJSON is a GET whose 200 body decodes into out; anything else is an
// error carrying the node's API error.
func (n *node) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := n.do(ctx, http.MethodGet, path, query, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readAPIError(resp)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

// readAPIError turns a non-2xx response into an *api.Error.
func readAPIError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var eb api.ErrorBody
	if json.Unmarshal(data, &eb) != nil || eb.Error.Code == "" {
		eb.Error = api.ErrorDetail{Code: api.CodeInternal, Message: resp.Status}
	}
	return &api.Error{Status: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
}

// Status is GET /v1/node, called with the node's token. It lives here, not
// in the api client, so the gateway does not depend on a client method a
// node version may not have.
func (n *node) Status(ctx context.Context) (api.NodeStatus, error) {
	var st api.NodeStatus
	err := n.getJSON(ctx, "/v1/node", nil, &st)
	return st, err
}

func (n *node) info() api.NodeInfo {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := api.NodeInfo{Name: n.cfg.Name, Endpoint: n.cfg.Endpoint, Healthy: n.healthy, LastSeen: n.lastSeen, Error: n.lastErr}
	if n.status != nil {
		st := *n.status
		out.Status = &st
	}
	return out
}

func (n *node) isHealthy() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.healthy
}

// candidate is the node as the scheduler sees it, if it is healthy.
func (n *node) candidate() (Candidate, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.healthy || n.status == nil {
		return Candidate{}, false
	}
	c := Candidate{Name: n.cfg.Name, Status: *n.status}
	// A node the gateway cordoned and that restarted without it gets
	// nothing new before the cordon is back (drain.go).
	c.Status.Cordoned = c.Status.Cordoned || n.wantCordon
	for p := range n.placed {
		c.Reserved.CPUs += p.res.CPUs
		c.Reserved.MemoryMB += p.res.MemoryMB
		c.Reserved.DiskMB += p.res.DiskMB
	}
	return c, true
}

func (n *node) reserve(res api.NodeResources) *placement {
	p := &placement{res: res, sent: time.Now()}
	n.mu.Lock()
	n.placed[p] = struct{}{}
	n.mu.Unlock()
	return p
}

// finish ends a placement: a failed create releases it at once; a created
// sandbox stays reserved until a status taken after it arrives.
func (n *node) finish(p *placement, created bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !created {
		delete(n.placed, p)
		return
	}
	p.done = time.Now()
}

// poolConfig is how the pool polls.
type poolConfig struct {
	interval  time.Duration
	failAfter int
	newClient func(NodeConfig) (*api.Client, error)
	logf      func(string, ...any)
	// changed, when set, is told when a node becomes healthy or unhealthy,
	// after the poll that decided it has let go of the node's lock; and
	// polled after every successful poll.
	changed func(n *node, healthy bool)
	polled  func(n *node, st api.NodeStatus)
}

// nodePool is every node the gateway knows, polled for health and capacity.
type nodePool struct {
	cfg   poolConfig
	mu    sync.RWMutex
	nodes map[string]*node
}

func newNodePool(cfg poolConfig) *nodePool {
	if cfg.interval <= 0 {
		cfg.interval = 5 * time.Second
	}
	if cfg.failAfter <= 0 {
		cfg.failAfter = 3
	}
	if cfg.newClient == nil {
		cfg.newClient = NewNodeClient
	}
	if cfg.logf == nil {
		cfg.logf = func(string, ...any) {}
	}
	return &nodePool{cfg: cfg, nodes: map[string]*node{}}
}

// add builds a client for cfg and adds the node, unhealthy until it answers.
func (p *nodePool) add(cfg NodeConfig, static bool) (*node, error) {
	c, err := p.cfg.newClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("node %s: %w", cfg.Name, err)
	}
	n := newNode(cfg, static, c)
	p.mu.Lock()
	defer p.mu.Unlock()
	if o, ok := p.nodes[cfg.Name]; ok && o.static && !static {
		return nil, fmt.Errorf("node %s is defined in the node config file", cfg.Name)
	}
	p.nodes[cfg.Name] = n
	return n, nil
}

func (p *nodePool) remove(name string) {
	p.mu.Lock()
	delete(p.nodes, name)
	p.mu.Unlock()
}

func (p *nodePool) get(name string) *node {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nodes[name]
}

// list returns the nodes sorted by name.
func (p *nodePool) list() []*node {
	p.mu.RLock()
	out := make([]*node, 0, len(p.nodes))
	for _, n := range p.nodes {
		out = append(out, n)
	}
	p.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].cfg.Name < out[j].cfg.Name })
	return out
}

func (p *nodePool) healthy() []*node {
	var out []*node
	for _, n := range p.list() {
		if n.isHealthy() {
			out = append(out, n)
		}
	}
	return out
}

func (p *nodePool) candidates() []Candidate {
	var out []Candidate
	for _, n := range p.list() {
		if c, ok := n.candidate(); ok {
			out = append(out, c)
		}
	}
	return out
}

// pollAll polls every node in parallel and returns when all have answered
// or timed out.
func (p *nodePool) pollAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, n := range p.list() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.poll(ctx, n)
		}()
	}
	wg.Wait()
}

// poll asks one node for its status. A node that fails failAfter polls in a
// row is unhealthy and gets no new sandboxes; one that has never answered is
// unhealthy from the start. A node reporting another node's name is
// unhealthy at once: its ids would route to the wrong place.
func (p *nodePool) poll(ctx context.Context, n *node) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.interval+2*time.Second)
	defer cancel()
	asked := time.Now()
	st, err := n.Status(ctx)
	if err == nil && st.Node != "" && st.Node != n.cfg.Name {
		err = fmt.Errorf("the node calls itself %q; it is configured as %q", st.Node, n.cfg.Name)
		n.mu.Lock()
		n.fails = p.cfg.failAfter
		n.mu.Unlock()
	}
	// Run after the unlock below: the callbacks take locks of their own.
	var after []func()
	defer func() {
		for _, f := range after {
			f()
		}
	}()
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.fails++
		n.lastErr = err.Error()
		if n.healthy && n.fails >= p.cfg.failAfter {
			n.healthy = false
			p.cfg.logf("node %s is not answering; it gets no new sandboxes: %v", n.cfg.Name, err)
			if p.cfg.changed != nil {
				after = append(after, func() { p.cfg.changed(n, false) })
			}
		}
		return
	}
	if !n.healthy {
		p.cfg.logf("node %s is answering", n.cfg.Name)
		if p.cfg.changed != nil {
			after = append(after, func() { p.cfg.changed(n, true) })
		}
	}
	if p.cfg.polled != nil {
		after = append(after, func() { p.cfg.polled(n, st) })
	}
	n.healthy, n.fails, n.lastErr = true, 0, ""
	n.lastSeen = time.Now().UTC()
	n.downSince = n.lastSeen
	// A create in flight when the status was asked for may or may not be in
	// its Free, and counting it both there and as a placement refuses
	// creates there is room for — a burst near capacity would be turned
	// away. So while one is, the last exact Free is kept, with every
	// placement since; the rest of the status is taken. A node never quiet
	// between polls has its Free taken anyway after a few, erring toward
	// counting a create twice.
	ambiguous := false
	for pl := range n.placed {
		if pl.sent.Before(asked) && (pl.done.IsZero() || !pl.done.Before(asked)) {
			ambiguous = true
		}
	}
	if ambiguous && n.status != nil && n.heldFree < maxHeldFree {
		n.heldFree++
		st.Free = n.status.Free
		n.status = &st
		return
	}
	n.heldFree = 0
	n.status = &st
	// What finished before this status was asked for is in it now.
	for pl := range n.placed {
		if !pl.done.IsZero() && pl.done.Before(asked) {
			delete(n.placed, pl)
		}
	}
}

// maxHeldFree is how many polls in a row a node's Free may be kept.
const maxHeldFree = 10

// run polls every interval until ctx ends.
func (p *nodePool) run(ctx context.Context) {
	t := time.NewTicker(p.cfg.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollAll(ctx)
		}
	}
}

// --- reaching a node ------------------------------------------------------------

// CheckNodeConfig checks a node's name and endpoint. A node is reached over
// https, a unix socket, or plain http on loopback only: the node's token
// travels with every request, and on a network in the clear it is anyone's.
func CheckNodeConfig(cfg NodeConfig) error {
	if !api.ValidNodeID(cfg.Name) {
		return fmt.Errorf("node name %q: lowercase letters, digits and dashes, at most 31, as in its sandboxes' ids", cfg.Name)
	}
	if strings.HasPrefix(cfg.Endpoint, "unix://") {
		if strings.TrimPrefix(cfg.Endpoint, "unix://") == "" {
			return fmt.Errorf("endpoint %q names no socket", cfg.Endpoint)
		}
		return nil
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("endpoint %q: want https://host:port or unix:///path", cfg.Endpoint)
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("endpoint %q: plain http is for a node on this machine only; use https", cfg.Endpoint)
		}
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return errors.New("a client certificate needs both cert_file and key_file")
	}
	if u.Scheme != "https" && (cfg.CAFile != "" || cfg.CertFile != "") {
		return fmt.Errorf("endpoint %q: CA and client certificates are for https", cfg.Endpoint)
	}
	return nil
}

// NewNodeClient builds the client for one node: its token from TokenFile,
// and for https the CA in CAFile (instead of the system's) and the
// gateway's client certificate for mutual TLS.
func NewNodeClient(cfg NodeConfig) (*api.Client, error) {
	if err := CheckNodeConfig(cfg); err != nil {
		return nil, err
	}
	token, err := readSecretFile(cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	switch {
	case strings.HasPrefix(cfg.Endpoint, "unix://"):
		return api.NewClient(cfg.Endpoint, token)
	case strings.HasPrefix(cfg.Endpoint, "http://"):
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.MaxIdleConnsPerHost = 64
		return api.NewClientWithHTTP(cfg.Endpoint, token, &http.Client{Transport: tr}), nil
	}
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates in %s", cfg.CAFile)
		}
		tc.RootCAs = pool
	}
	if cfg.CertFile != "" {
		if err := checkPrivate(cfg.KeyFile); err != nil {
			return nil, err
		}
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, err
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return api.NewClientWithTLS(cfg.Endpoint, token, tc)
}

// readSecretFile reads a token, refusing a file others can read, as sandboxd
// does for its own: a token every account can see protects nothing.
func readSecretFile(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if err := checkPrivate(path); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if tok == "" || strings.ContainsAny(tok, "\r\n") {
		return "", fmt.Errorf("%s does not hold a token on one line", path)
	}
	return tok, nil
}

func checkPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by others (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	return nil
}
