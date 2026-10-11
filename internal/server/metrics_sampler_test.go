package server

import (
	"context"
	"math"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func metricsServer(t *testing.T, be backend.Backend) (*api.Client, *Server, func(time.Duration)) {
	t.Helper()
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC).UnixNano())
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), MetricsInterval: time.Hour,
		now: func() time.Time { return time.Unix(0, clock.Load()) }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, s, func(d time.Duration) { clock.Add(int64(d)) }
}

// A sample is the host's reading of a running sandbox: the CPU share is the
// time used since the last reading over the time between them and the vCPUs
// given; memory carries its limit; an hour is kept, and older samples go.
func TestMetricsSampleTheLastHour(t *testing.T) {
	c, s, advance := metricsServer(t, fake.New(api.CapEgressAllowlist))
	ctx := context.Background()
	caps, err := c.Capabilities(ctx)
	if err != nil || !caps.Has(api.CapMetrics) {
		t.Fatalf("capabilities %v, %v; want metrics where the backend reads usage", caps.Capabilities, err)
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2, MemoryMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	s.sampleMetrics(ctx)
	advance(10 * time.Second)
	s.sampleMetrics(ctx)
	m, err := c.Metrics(ctx, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Samples) != 2 {
		t.Fatalf("%d samples; want 2", len(m.Samples))
	}
	// The fake uses 20 ms of CPU a reading with no process running: over 10 s
	// of 2 vCPUs, 0.1%.
	if got := m.Samples[1].CPUPercent; math.Abs(got-0.1) > 1e-9 || m.Samples[0].CPUPercent != 0 {
		t.Fatalf("cpu %v then %v; want 0 then 0.1", m.Samples[0].CPUPercent, got)
	}
	if m.Samples[1].MemoryBytes != 64<<20 || m.Samples[1].MemoryLimitBytes != 1<<30 {
		t.Fatalf("memory %d of %d", m.Samples[1].MemoryBytes, m.Samples[1].MemoryLimitBytes)
	}
	advance(61 * time.Minute)
	s.sampleMetrics(ctx)
	if m, _ := c.Metrics(ctx, sb.ID); len(m.Samples) != 1 {
		t.Fatalf("%d samples after an hour; want only the new one", len(m.Samples))
	}
}

// Reading a sandbox's metrics is not using it: a dashboard open on it must
// not keep it from idling out.
func TestReadingMetricsIsNotActivity(t *testing.T) {
	c, s, advance := metricsServer(t, fake.New(api.CapEgressAllowlist))
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := s.find(sb.ID)
	rec.mu.Lock()
	before := rec.lastActive
	rec.mu.Unlock()
	advance(time.Minute)
	if _, err := c.Metrics(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	after := rec.lastActive
	rec.mu.Unlock()
	if !after.Equal(before) {
		t.Fatal("reading metrics counted as activity")
	}
}

// noUsage is a backend that cannot read usage: the fake with Usage hidden.
type noUsage struct{ backend.Backend }

// An endpoint whose backend cannot measure usage offers no metrics, and its
// endpoint says so rather than serving an empty hour.
func TestMetricsNeedABackendThatReadsUsage(t *testing.T) {
	c, _, _ := metricsServer(t, noUsage{fake.New(api.CapEgressAllowlist)})
	ctx := context.Background()
	if caps, _ := c.Capabilities(ctx); caps.Has(api.CapMetrics) {
		t.Fatal("metrics offered without a usage reader")
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Metrics(ctx, sb.ID)
	if e, ok := err.(*api.Error); !ok || e.Code != api.CodeUnsupported {
		t.Fatalf("err %v; want unsupported", err)
	}
}

// Unset, usage is sampled every five seconds: an hour is 720 samples.
func TestMetricsIntervalDefaultsToFiveSeconds(t *testing.T) {
	if got := (&Server{}).metricsInterval(); got != 5*time.Second {
		t.Fatalf("default interval %v; want 5s", got)
	}
	if got := (&Server{MetricsInterval: time.Minute}).metricsInterval(); got != time.Minute {
		t.Fatalf("a set interval is %v; want it kept", got)
	}
}

// An ended sandbox keeps its last hour, readable, as its docs say; nothing
// samples it any more.
func TestAnEndedSandboxKeepsItsMetrics(t *testing.T) {
	c, s, advance := metricsServer(t, fake.New(api.CapEgressAllowlist))
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	s.sampleMetrics(ctx)
	advance(5 * time.Second)
	s.sampleMetrics(ctx)
	if err := c.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	advance(5 * time.Second)
	s.sampleMetrics(ctx)
	m, err := c.Metrics(ctx, sb.ID)
	if err != nil || len(m.Samples) != 2 {
		t.Fatalf("after it ended: %d samples, %v; want the 2 it had", len(m.Samples), err)
	}
}
