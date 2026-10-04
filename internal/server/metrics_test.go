package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

func scrape(t *testing.T, s *Server) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Host = "127.0.0.1:9100"
	rec := httptest.NewRecorder()
	s.MetricsHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != metrics.ContentType {
		t.Fatalf("GET /metrics: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

func TestNodeMetrics(t *testing.T) {
	c, _, s := nodeServer(t, "n1", 0)
	ctx := context.Background()
	a, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", CPUs: 2, MemoryMB: 4096, DiskMB: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"}); !api.IsCode(err, api.CodeConflict) {
		t.Fatalf("a second web: %v", err)
	}
	if _, err := c.StartProcess(ctx, a.ID, api.RunRequest{Argv: []string{"sleep", "30"}}); err != nil {
		t.Fatal(err)
	}
	b, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TerminateSandbox(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	got := scrape(t, s)
	for _, want := range []string{
		`sandboxd_sandboxes{state="running"} 1`,
		`sandboxd_sandboxes{state="terminated"} 1`,
		`sandboxd_sandboxes{state="pending"} 0`,
		"sandboxd_processes_running 1",
		`sandboxd_creates_total{code="201"} 2`,
		`sandboxd_creates_total{code="409"} 1`,
		"sandboxd_create_seconds_count 2",
		"sandboxd_capacity_cpus 8",
		"sandboxd_free_cpus 6",
		"sandboxd_cordoned 0",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// A sandbox's name is the client's: it is no label.
	if strings.Contains(got, "web") || strings.Contains(got, a.ID) {
		t.Errorf("metrics carry a client-chosen string:\n%s", got)
	}
}

func TestNodeMetricsPools(t *testing.T) {
	_, _, s := nodeServer(t, "n1", 2)
	got := scrape(t, s)
	img := s.Policy.DefaultImage
	if !strings.Contains(got, `sandboxd_pool_target{image="`+img+`"} 2`) {
		t.Errorf("pool target missing:\n%s", got)
	}
}
