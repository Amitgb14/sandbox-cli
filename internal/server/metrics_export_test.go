package server

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// sink is a user's collector: it keeps each push and answers status.
type sink struct {
	mu     sync.Mutex
	pushes []string
	auth   []string
	status int
	got    chan struct{}
}

func newSink(t *testing.T) (*sink, string) {
	k := &sink{got: make(chan struct{}, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(zr)
		k.mu.Lock()
		k.pushes = append(k.pushes, string(b))
		k.auth = append(k.auth, r.Header.Get("Authorization"))
		status := k.status
		k.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
		}
		k.got <- struct{}{}
	}))
	t.Cleanup(srv.Close)
	return k, srv.URL
}

func (k *sink) wait(t *testing.T) {
	t.Helper()
	select {
	case <-k.got:
	case <-time.After(10 * time.Second):
		t.Fatal("no push arrived")
	}
}

func (k *sink) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.pushes)
}

func exportServer(t *testing.T, be backend.Backend, allowPrivate bool) (*api.Client, *Server, func(time.Duration), string) {
	t.Helper()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC).UnixNano())
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), MetricsInterval: time.Hour,
		MetricsExportAllowPrivate: allowPrivate, Audit: audit.NewLog(logPath),
		now: func() time.Time { return time.Unix(0, clock.Load()) }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, s, func(d time.Duration) { clock.Add(int64(d)) }, logPath
}

// waitIdle waits for the pushes in flight to record how they went.
func waitIdle(t *testing.T, s *Server, id string) api.Sandbox {
	t.Helper()
	for i := 0; i < 500; i++ {
		rec, _ := s.find(id)
		rec.mu.Lock()
		busy := rec.export.inFlight
		out := rec.sbx
		rec.mu.Unlock()
		if !busy {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("a push stayed in flight")
	return api.Sandbox{}
}

// The whole path: a sandbox asks for its usage at its own collector, gets
// one push per interval with a sample it has not sent, carrying its
// credential, and GET shows the export by header name only.
func TestMetricsExportPushesASandboxsUsage(t *testing.T) {
	k, url := newSink(t)
	c, s, advance, logPath := exportServer(t, fake.New(api.CapEgressAllowlist), true)
	ctx := context.Background()
	if caps, _ := c.Capabilities(ctx); !caps.Has(api.CapMetricsExport) {
		t.Fatal("metrics_export not offered")
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", Labels: map[string]string{"team": "a", "gateway.user": "u1"},
		MetricsExport: &api.MetricsExport{Endpoint: url, IntervalSecs: 30, Headers: map[string]string{"authorization": "Bearer s3cret"}}})
	if err != nil {
		t.Fatal(err)
	}
	st := sb.MetricsExport
	if st == nil || st.Endpoint != url+"/v1/metrics" || st.IntervalSecs != 30 || strings.Join(st.HeaderNames, ",") != "Authorization" {
		t.Fatalf("status %+v", st)
	}
	raw, _ := json.Marshal(sb)
	if strings.Contains(string(raw), "s3cret") {
		t.Fatalf("a header value came back: %s", raw)
	}

	s.sampleMetrics(ctx)
	k.wait(t)
	got := waitIdle(t, s, sb.ID)
	if got.MetricsExport.LastPushAt == nil || got.MetricsExport.LastError != "" {
		t.Fatalf("after a push: %+v", got.MetricsExport)
	}
	body := k.pushes[0]
	for _, want := range []string{
		`{"key":"sandbox.id","value":{"stringValue":"` + sb.ID + `"}}`,
		`{"key":"sandbox.name","value":{"stringValue":"web"}}`,
		`{"key":"sandbox.label.team","value":{"stringValue":"a"}}`,
		`"name":"sandbox.cpu.utilization"`,
		`"name":"sandbox.memory.usage"`,
		`"name":"sandbox.memory.limit"`,
		`"name":"sandbox.network.io"`,
		`{"key":"network.io.direction","value":{"stringValue":"transmit"}}`,
		`"name":"sandbox.disk.io"`,
		`"name":"sandbox.processes"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in\n%s", want, body)
		}
	}
	if strings.Contains(body, "gateway.user") {
		t.Error("a gateway label was pushed")
	}
	if k.auth[0] != "Bearer s3cret" {
		t.Errorf("Authorization %q", k.auth[0])
	}

	// Within the interval, a new sample waits; past it, it goes.
	advance(10 * time.Second)
	s.sampleMetrics(ctx)
	waitIdle(t, s, sb.ID)
	if k.count() != 1 {
		t.Fatalf("%d pushes inside the interval", k.count())
	}
	advance(30 * time.Second)
	s.sampleMetrics(ctx)
	k.wait(t)

	// Nothing of the credential is in the audit log; where it goes is.
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "s3cret") || !strings.Contains(string(log), `"metrics_endpoint":"`+url+`/v1/metrics"`) {
		t.Fatalf("audit log:\n%s", log)
	}
}

// A failed push is the sandbox's status, with when it began to fail, and
// clears on the next one that works.
func TestMetricsExportReportsFailure(t *testing.T) {
	k, url := newSink(t)
	k.status = http.StatusServiceUnavailable
	c, s, advance, _ := exportServer(t, fake.New(api.CapEgressAllowlist), true)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: url, IntervalSecs: 10}})
	if err != nil {
		t.Fatal(err)
	}
	s.sampleMetrics(ctx)
	k.wait(t)
	got := waitIdle(t, s, sb.ID).MetricsExport
	if got.LastError != "503 Service Unavailable" || got.FailingSince == nil || got.LastPushAt != nil {
		t.Fatalf("after a refused push: %+v", got)
	}
	since := *got.FailingSince
	// A sample not accepted is sent again, with the next, rather than lost.
	advance(10 * time.Second)
	s.sampleMetrics(ctx)
	k.wait(t)
	if got := waitIdle(t, s, sb.ID).MetricsExport; !got.FailingSince.Equal(since) {
		t.Fatalf("failing since moved: %v, was %v", got.FailingSince, since)
	}
	k.mu.Lock()
	k.status = 0
	k.mu.Unlock()
	advance(10 * time.Second)
	s.sampleMetrics(ctx)
	k.wait(t)
	if got := waitIdle(t, s, sb.ID).MetricsExport; got.LastError != "" || got.FailingSince != nil || got.LastPushAt == nil {
		t.Fatalf("after a push that worked: %+v", got)
	}
}

// Without the operator's flag, the host pushes to public addresses only: a
// URL naming loopback is refused when it is sent, and one that only
// resolves there would be at the dial (metrics.TestClientPusherRefusesLoopback).
func TestMetricsExportRefusals(t *testing.T) {
	c, _, _, _ := exportServer(t, fake.New(api.CapEgressAllowlist), false)
	ctx := context.Background()
	for name, me := range map[string]*api.MetricsExport{
		"loopback":       {Endpoint: "https://127.0.0.1:4318"},
		"metadata":       {Endpoint: "https://169.254.169.254/"},
		"plain http":     {Endpoint: "http://collector.example:4318"},
		"query":          {Endpoint: "https://collector.example/?key=s3cret"},
		"too often":      {Endpoint: "https://collector.example", IntervalSecs: 1},
		"bad header":     {Endpoint: "https://collector.example", Headers: map[string]string{"Host": "s3cret"}},
		"no endpoint":    {Headers: map[string]string{"Authorization": "s3cret"}},
		"header secrets": {Endpoint: "https://collector.example", HeaderSecrets: map[string]string{"Authorization": "otel"}},
	} {
		_, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: me})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the error repeats a credential: %v", name, err)
		}
	}
	// Refused where usage is not measured, rather than accepted and silent.
	c2, _, _, _ := exportServer(t, noUsage{fake.New(api.CapEgressAllowlist)}, true)
	if caps, _ := c2.Capabilities(ctx); caps.Has(api.CapMetricsExport) {
		t.Fatal("metrics_export offered without a usage reader")
	}
	_, err := c2.CreateSandbox(ctx, api.CreateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: "https://collector.example"}})
	if e, ok := err.(*api.Error); !ok || e.Code != api.CodeUnsupported {
		t.Fatalf("without usage: %v", err)
	}
}

// An update sets, replaces and stops the export, each recorded.
func TestMetricsExportUpdate(t *testing.T) {
	k, url := newSink(t)
	c, s, _, logPath := exportServer(t, fake.New(api.CapEgressAllowlist), true)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	s.sampleMetrics(ctx) // a sample from before the export is never sent
	up, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: url, Headers: map[string]string{"X-Key": "k1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if up.MetricsExport == nil || up.MetricsExport.IntervalSecs != spec.DefaultMetricsExportSecs {
		t.Fatalf("after set: %+v", up.MetricsExport)
	}
	s.pushExports(s.now())
	waitIdle(t, s, sb.ID)
	if k.count() != 0 {
		t.Fatal("a sample from before the export was pushed")
	}
	// A bad export changes nothing.
	if _, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: "ftp://x"}}); err == nil {
		t.Fatal("a bad endpoint was accepted")
	}
	if g, _ := c.Sandbox(ctx, sb.ID); g.MetricsExport == nil || g.MetricsExport.Endpoint != url+"/v1/metrics" {
		t.Fatalf("after a refused update: %+v", g.MetricsExport)
	}
	up, err = c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: ""}})
	if err != nil || up.MetricsExport != nil {
		t.Fatalf("after stop: %+v, %v", up.MetricsExport, err)
	}
	s.sampleMetrics(ctx)
	waitIdle(t, s, sb.ID)
	if k.count() != 0 {
		t.Fatal("pushed after the export stopped")
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), `"metrics_endpoint":"none"`) || strings.Contains(string(log), "k1") {
		t.Fatalf("audit log:\n%s", log)
	}
}

// A kept sandbox keeps its export, header values included, across a
// restart; they are in its record, as its environment is.
func TestMetricsExportIsKept(t *testing.T) {
	k, url := newSink(t)
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	newServer := func() *Server {
		s := &Server{Backend: b, Policy: spec.DefaultPolicyFor(b.Capabilities()), RecordDir: dir, MetricsExportAllowPrivate: true, MetricsInterval: time.Hour}
		if _, err := s.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s1 := newServer()
	sb := mustCreate(t, s1.Handler(), api.CreateSandboxRequest{MetricsExport: &api.MetricsExport{Endpoint: url, Headers: map[string]string{"Authorization": "Bearer kept"}}})
	b.Detach()
	s2 := newServer()
	t.Cleanup(func() { _ = b.Terminate(context.Background(), sb.ID) })
	r := call(t, s2.Handler(), "GET", "/v1/sandboxes/"+sb.ID, nil)
	if !strings.Contains(r.Body.String(), `"header_names":["Authorization"]`) || strings.Contains(r.Body.String(), "kept") {
		t.Fatalf("after restart: %s", r.Body)
	}
	s2.sampleMetrics(context.Background())
	k.wait(t)
	if k.auth[0] != "Bearer kept" {
		t.Fatalf("Authorization after restart %q", k.auth[0])
	}
}
