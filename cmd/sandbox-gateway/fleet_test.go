package main

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// Metrics have no credential, so they are served on loopback only.
func TestServeRefusesMetricsOffLoopback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := serve(ctx, filepath.Join(t.TempDir(), "state.json"),
		serveOptions{listen: "127.0.0.1:0", metricsListen: "0.0.0.0:0"}, t.Logf, func(string) { cancel() })
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("err = %v", err)
	}
}

// serve with metrics and the default audit log: the metrics answer without
// a key, and a request made with one is in the log beside the state file.
func TestServeMetricsAndAudit(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	out, err := run(t, "--state", state, "keys", "create", "--user", "root", "--scope", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret := regexp.MustCompile(`secret: (sgk_\S+)`).FindStringSubmatch(out)[1]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	where, metricsAt := make(chan string, 1), make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, state, serveOptions{listen: "127.0.0.1:0", pollInterval: time.Second,
			metricsListen: "127.0.0.1:0", metricsReady: func(a string) { metricsAt <- a }},
			t.Logf, func(w string) { where <- w })
	}()
	var base, maddr string
	select {
	case base = <-where:
		maddr = <-metricsAt
	case err := <-done:
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/admin/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get("http://" + maddr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `sandbox_gateway_http_requests_total{route="GET /v1/admin/nodes",code="200"} 1`) {
		t.Fatalf("metrics %d:\n%s", resp.StatusCode, body)
	}
	// The API's listener has no /metrics.
	resp, err = http.Get(base + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /metrics on the API: %d", resp.StatusCode)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "audit", "gateway.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"action":"GET /v1/admin/nodes"`) || strings.Contains(string(data), secret) {
		t.Fatalf("audit log:\n%s", data)
	}
}

// serve --otlp-endpoint pushes the gateway's metrics, and once more on the
// way out, so a request made just before a stop is not lost.
func TestServePushesMetricsOverOTLP(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	out, err := run(t, "--state", state, "keys", "create", "--user", "root", "--scope", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret := regexp.MustCompile(`secret: (sgk_\S+)`).FindStringSubmatch(out)[1]
	var mu sync.Mutex
	var pushes []string
	col := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(zr)
		mu.Lock()
		pushes = append(pushes, string(b)+"\n"+r.Header.Get("Authorization"))
		mu.Unlock()
	}))
	defer col.Close()
	hf := filepath.Join(dir, "otlp-headers")
	if err := os.WriteFile(hf, []byte("Authorization: Bearer collector-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	where := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, state, serveOptions{listen: "127.0.0.1:0", pollInterval: time.Second,
			otlp: metrics.OTLPConfig{Endpoint: col.URL, Interval: time.Hour, HeadersFile: hf}},
			t.Logf, func(w string) { where <- w })
	}()
	var base string
	select {
	case base = <-where:
	case err := <-done:
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/admin/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(pushes) != 1 {
		t.Fatalf("%d pushes, want the one on the way out", len(pushes))
	}
	for _, want := range []string{
		`{"key":"service.name","value":{"stringValue":"sandbox-gateway"}}`,
		`"name":"sandbox_gateway_http_requests_total"`,
		`{"key":"route","value":{"stringValue":"GET /v1/admin/nodes"}}`,
		"Bearer collector-key",
	} {
		if !strings.Contains(pushes[0], want) {
			t.Errorf("missing %s in\n%s", want, pushes[0])
		}
	}
	if strings.Contains(pushes[0], secret) {
		t.Error("an API key reached the collector")
	}
}

// Checked before anything is opened, and before a standby gateway waits.
func TestServeRefusesPlainOTLPOffLoopback(t *testing.T) {
	err := serve(context.Background(), filepath.Join(t.TempDir(), "state.json"),
		serveOptions{listen: "127.0.0.1:0", otlp: metrics.OTLPConfig{Endpoint: "http://collector.example:4318"}}, t.Logf, nil)
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("err = %v", err)
	}
}
