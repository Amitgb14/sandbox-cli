package gateway

import (
	"net/http"
	"sync/atomic"

	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// GET /metrics, served on a listener of its own (sandbox-gateway serve
// --metrics-listen, loopback only, off by default) and not on the API's:
// the API's every route needs a key, and a scraper holding an admin key
// would be one more place an admin key lives.
//
// Label values are bounded: a route is the pattern the handler was
// registered with (never the path a request sent), a code is a status, a
// node is a name the operator configured and api.ValidNodeID allows, and a
// reason is one this file defines. No user, tenant, key, sandbox or SSH
// username appears: a label a client could choose would be a series per
// request, and a channel from a client into the operator's monitoring.

// Reasons a create was refused before it reached a node.
const (
	refusedQuota    = "quota"
	refusedCapacity = "no_capacity"
)

type gatewayMetrics struct {
	requests   *metrics.CounterVec // route, code
	created    *metrics.CounterVec
	terminated *metrics.CounterVec // how: delete, ended, drain
	refused    *metrics.CounterVec // reason
	scheduled  *metrics.CounterVec // node
	createLat  *metrics.Histogram

	sshConns    atomic.Int64
	sshSessions atomic.Int64
	sshAuthFail *metrics.CounterVec
}

func newGatewayMetrics() *gatewayMetrics {
	return &gatewayMetrics{
		requests:    metrics.NewCounterVec("route", "code"),
		created:     metrics.NewCounterVec(),
		terminated:  metrics.NewCounterVec("how"),
		refused:     metrics.NewCounterVec("reason"),
		scheduled:   metrics.NewCounterVec("node"),
		createLat:   metrics.NewHistogram(metrics.LatencyBuckets...),
		sshAuthFail: metrics.NewCounterVec(),
	}
}

// SSHMetrics is what the SSH server reports to: connections and sessions
// open, and failed logins. A nil one counts nothing.
type SSHMetrics struct{ m *gatewayMetrics }

func (s *SSHMetrics) connOpened() {
	if s != nil {
		s.m.sshConns.Add(1)
	}
}

func (s *SSHMetrics) connClosed() {
	if s != nil {
		s.m.sshConns.Add(-1)
	}
}

func (s *SSHMetrics) sessionOpened() {
	if s != nil {
		s.m.sshSessions.Add(1)
	}
}

func (s *SSHMetrics) sessionClosed() {
	if s != nil {
		s.m.sshSessions.Add(-1)
	}
}

func (s *SSHMetrics) authFailed() {
	if s != nil {
		s.m.sshAuthFail.Inc()
	}
}

// SSHMetrics returns the gateway's counters for its SSH server
// (SSHConfig.Metrics).
func (g *Gateway) SSHMetrics() *SSHMetrics { return &SSHMetrics{m: g.metrics} }

// MetricsHandler serves GET /metrics in the Prometheus text format.
func (g *Gateway) MetricsHandler() http.Handler { return metrics.Handler(g.writeMetrics) }

func (g *Gateway) writeMetrics(w *metrics.Writer) {
	m := g.metrics
	m.requests.Write(w, "sandbox_gateway_http_requests_total", "API requests, by route pattern and response status.")
	m.created.Write(w, "sandbox_gateway_sandboxes_created_total", "Sandboxes created through the gateway.")
	m.terminated.Write(w, "sandbox_gateway_sandboxes_terminated_total", "Sandboxes the gateway stopped recording: deleted through it, found ended on their node, or terminated by a drain.")
	m.refused.Write(w, "sandbox_gateway_creates_refused_total", "Creates refused before reaching a node, by reason.")
	m.scheduled.Write(w, "sandbox_gateway_scheduled_total", "Scheduler decisions, by the node chosen.")
	m.createLat.Write(w, "sandbox_gateway_create_seconds", "Time to create a sandbox through the gateway, successful creates only.")

	nodes := g.nodes.list()
	w.Family("sandbox_gateway_node_up", "gauge", "1 when the node answers its status polls.")
	for _, n := range nodes {
		w.Sample("sandbox_gateway_node_up", b2f(n.isHealthy()), "node", n.cfg.Name)
	}
	w.Family("sandbox_gateway_node_cordoned", "gauge", "1 when the node's last status said it is cordoned.")
	for _, n := range nodes {
		info := n.info()
		w.Sample("sandbox_gateway_node_cordoned", b2f(info.Status != nil && info.Status.Cordoned), "node", n.cfg.Name)
	}
	type res struct {
		name, help string
		get        func(*nodeView) float64
	}
	for _, r := range []res{
		{"sandbox_gateway_node_capacity_cpus", "CPUs the node offers sandboxes, from its last status.", func(v *nodeView) float64 { return v.capCPUs }},
		{"sandbox_gateway_node_free_cpus", "CPUs free on the node, from its last status.", func(v *nodeView) float64 { return v.freeCPUs }},
		{"sandbox_gateway_node_capacity_memory_mb", "Memory the node offers sandboxes, MiB.", func(v *nodeView) float64 { return v.capMem }},
		{"sandbox_gateway_node_free_memory_mb", "Memory free on the node, MiB.", func(v *nodeView) float64 { return v.freeMem }},
		{"sandbox_gateway_node_capacity_disk_mb", "Disk the node offers sandboxes, MiB.", func(v *nodeView) float64 { return v.capDisk }},
		{"sandbox_gateway_node_free_disk_mb", "Disk free on the node, MiB.", func(v *nodeView) float64 { return v.freeDisk }},
		{"sandbox_gateway_node_running", "Sandboxes running on the node, from its last status.", func(v *nodeView) float64 { return v.running }},
	} {
		w.Family(r.name, "gauge", r.help)
		for _, n := range nodes {
			if v := viewOf(n); v != nil {
				w.Sample(r.name, r.get(v), "node", n.cfg.Name)
			}
		}
	}

	lost := g.lostSandboxes()
	w.Gauge("sandbox_gateway_sandboxes_recorded", "Sandboxes the gateway's store records an owner for.", float64(g.store.sandboxCount()))
	w.Gauge("sandbox_gateway_sandboxes_lost", "Recorded sandboxes on nodes past the grace period (GET /v1/admin/lost).", float64(len(lost)))

	w.Gauge("sandbox_gateway_ssh_connections_open", "SSH connections open.", float64(m.sshConns.Load()))
	w.Gauge("sandbox_gateway_ssh_sessions_open", "SSH session and forwarding channels open.", float64(m.sshSessions.Load()))
	m.sshAuthFail.Write(w, "sandbox_gateway_ssh_auth_failures_total", "SSH connections that presented a credential and were refused.")
}

// nodeView is a node's last status as numbers.
type nodeView struct {
	capCPUs, freeCPUs, capMem, freeMem, capDisk, freeDisk, running float64
}

func viewOf(n *node) *nodeView {
	info := n.info()
	if info.Status == nil {
		return nil
	}
	st := info.Status
	return &nodeView{
		capCPUs: st.Capacity.CPUs, freeCPUs: st.Free.CPUs,
		capMem: float64(st.Capacity.MemoryMB), freeMem: float64(st.Free.MemoryMB),
		capDisk: float64(st.Capacity.DiskMB), freeDisk: float64(st.Free.DiskMB),
		running: float64(st.Running),
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
