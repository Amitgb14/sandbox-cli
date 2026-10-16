package metrics

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
	"testing"
	"time"
)

// collector is an OTLP/HTTP endpoint that keeps what it was sent.
type collector struct {
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
	status  int
}

func (c *collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	b, _ := io.ReadAll(zr)
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies = append(c.bodies, body)
	c.headers = append(c.headers, r.Header.Clone())
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.bodies)
}

// metricsOf indexes a request's metrics by name.
func metricsOf(t *testing.T, body map[string]any) (map[string]map[string]any, []any) {
	t.Helper()
	rm := body["resourceMetrics"].([]any)[0].(map[string]any)
	res := rm["resource"].(map[string]any)["attributes"].([]any)
	sm := rm["scopeMetrics"].([]any)[0].(map[string]any)
	out := map[string]map[string]any{}
	for _, m := range sm["metrics"].([]any) {
		mm := m.(map[string]any)
		out[mm["name"].(string)] = mm
	}
	return out, res
}

func TestOTLPExport(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()

	reqs := NewCounterVec("route", "code")
	reqs.Add(3, "GET /v1/x", "200")
	unused := NewCounterVec("reason") // nothing counted: no metric at all
	plain := NewCounterVec()          // no labels: 0 before the first Add
	h := NewHistogram(1, 0.1)
	for _, v := range []float64{0.05, 0.5, 5} {
		h.Observe(v)
	}
	write := func(w *Writer) {
		reqs.Write(w, "reqs_total", "requests")
		unused.Write(w, "refused_total", "refused")
		plain.Write(w, "plain_total", "plain")
		h.Write(w, "lat_seconds", "latency")
		w.Family("pool", "gauge", "by image")
		w.Sample("pool", 2, "image", "a")
		w.Sample("pool", 1, "image", "b")
	}

	dir := t.TempDir()
	hf := filepath.Join(dir, "headers")
	os.WriteFile(hf, []byte("# the collector's key\nAuthorization: Bearer s3cret\n\nX-Scope-OrgID: ops\n"), 0o600)
	e, err := NewExporter(OTLPConfig{Endpoint: srv.URL, HeadersFile: hf, Service: "sandboxd", Version: "1.2.3", Instance: "n1"}, write)
	if err != nil {
		t.Fatal(err)
	}
	if e.URL() != srv.URL+"/v1/metrics" || e.Interval() != DefaultOTLPInterval {
		t.Errorf("url %s, interval %v", e.URL(), e.Interval())
	}
	if err := e.Export(context.Background()); err != nil {
		t.Fatal(err)
	}
	hd := c.headers[0]
	if hd.Get("Authorization") != "Bearer s3cret" || hd.Get("X-Scope-Orgid") != "ops" || hd.Get("Content-Type") != "application/json" {
		t.Errorf("headers %v", hd)
	}

	ms, res := metricsOf(t, c.bodies[0])
	b, _ := json.Marshal(res)
	for _, want := range []string{`"service.name"`, `"sandboxd"`, `"service.version"`, `"1.2.3"`, `"service.instance.id"`, `"n1"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("resource %s lacks %s", b, want)
		}
	}
	if _, ok := ms["refused_total"]; ok {
		t.Error("a counter with no series was sent")
	}

	sum := ms["reqs_total"]["sum"].(map[string]any)
	if sum["isMonotonic"] != true || sum["aggregationTemporality"] != float64(2) {
		t.Errorf("sum %v", sum)
	}
	p := sum["dataPoints"].([]any)[0].(map[string]any)
	if p["asDouble"] != float64(3) || p["startTimeUnixNano"] == nil {
		t.Errorf("point %v", p)
	}
	if _, ok := p["timeUnixNano"].(string); !ok {
		t.Errorf("timeUnixNano is not a string: %v", p["timeUnixNano"])
	}
	pb, _ := json.Marshal(p["attributes"])
	if string(pb) != `[{"key":"route","value":{"stringValue":"GET /v1/x"}},{"key":"code","value":{"stringValue":"200"}}]` {
		t.Errorf("attributes %s", pb)
	}
	if pp := ms["plain_total"]["sum"].(map[string]any)["dataPoints"].([]any); len(pp) != 1 {
		t.Errorf("plain counter points %v", pp)
	}

	gp := ms["pool"]["gauge"].(map[string]any)["dataPoints"].([]any)
	if len(gp) != 2 || ms["pool"]["description"] != "by image" {
		t.Errorf("gauge %v", ms["pool"])
	}

	hp := ms["lat_seconds"]["histogram"].(map[string]any)["dataPoints"].([]any)[0].(map[string]any)
	hb, _ := json.Marshal(map[string]any{"b": hp["bucketCounts"], "e": hp["explicitBounds"], "c": hp["count"], "s": hp["sum"]})
	// 0.05 in (,0.1], 0.5 in (0.1,1], 5 in (1,+Inf).
	if string(hb) != `{"b":["1","1","1"],"c":"3","e":[0.1,1],"s":5.55}` {
		t.Errorf("histogram %s", hb)
	}
}

func TestOTLPEndpointRefusals(t *testing.T) {
	for _, bad := range []string{
		"http://collector.example:4318",   // plain HTTP off the machine
		"http://10.0.0.5:4318/v1/metrics", // the same, by address
		"https://user:pw@collector.example",
		"https://collector.example/v1/metrics?api-key=s3cret",
		"https://collector.example/v1/metrics?",
		"ftp://collector.example",
		"collector.example:4318",
		"https://",
		"https://collector.example/v1/metrics#x",
	} {
		if _, err := CheckOTLPEndpoint(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for in, want := range map[string]string{
		"http://127.0.0.1:4318":                     "http://127.0.0.1:4318/v1/metrics",
		"http://localhost:4318/":                    "http://localhost:4318/v1/metrics",
		"http://[::1]:4318/custom":                  "http://[::1]:4318/custom",
		"https://collector.example":                 "https://collector.example/v1/metrics",
		"https://collector.example/otlp/v1/metrics": "https://collector.example/otlp/v1/metrics",
	} {
		got, err := CheckOTLPEndpoint(in)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NewExporter(OTLPConfig{Endpoint: "http://127.0.0.1:1", Interval: time.Millisecond}, func(*Writer) {}); err == nil {
		t.Error("a sub-second interval was accepted")
	}
}

func TestOTLPHeadersFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), mode)
		os.Chmod(p, mode)
		return p
	}
	cases := map[string]string{
		"open":     write("open", "Authorization: Bearer x\n", 0o644),
		"nocolon":  write("nocolon", "Bearer s3cret\n", 0o600),
		"badname":  write("badname", "Bad Name: x\n", 0o600),
		"host":     write("host", "Host: elsewhere.example\n", 0o600),
		"encoding": write("encoding", "Content-Encoding: identity\n", 0o600),
	}
	for name, p := range cases {
		_, err := NewExporter(OTLPConfig{Endpoint: "http://127.0.0.1:4318", HeadersFile: p}, func(*Writer) {})
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// A refusal names the line, never what is on it.
		if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "Bearer") {
			t.Errorf("%s: the error repeats the file: %v", name, err)
		}
	}
}

func TestOTLPDoesNotFollowRedirects(t *testing.T) {
	elsewhere := &collector{}
	other := httptest.NewServer(elsewhere)
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/v1/metrics", http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	e, err := NewExporter(OTLPConfig{Endpoint: srv.URL}, func(w *Writer) { w.Gauge("up", "up", 1) })
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Export(context.Background()); err == nil {
		t.Error("a redirect counted as a push")
	}
	if elsewhere.count() != 0 {
		t.Error("the redirect was followed")
	}
}

func TestOTLPRunPushesAndFlushes(t *testing.T) {
	c := &collector{status: http.StatusServiceUnavailable}
	srv := httptest.NewServer(c)
	defer srv.Close()
	var mu sync.Mutex
	var logs []string
	e, err := NewExporter(OTLPConfig{Endpoint: srv.URL, Interval: time.Second, Logf: func(f string, a ...any) {
		mu.Lock()
		logs = append(logs, f)
		mu.Unlock()
	}}, func(w *Writer) { w.Gauge("up", "up", 1) })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for c.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	c.mu.Lock()
	c.status = 0
	c.mu.Unlock()
	n := c.count()
	cancel()
	<-done
	if c.count() != n+1 {
		t.Errorf("pushes %d before the end, %d after; want one more on the way out", n, c.count())
	}
	mu.Lock()
	defer mu.Unlock()
	// Two failures make one line; the push on the way out recovers.
	if len(logs) != 2 || !strings.Contains(logs[0], "retrying") || !strings.Contains(logs[1], "again") {
		t.Errorf("logs %q", logs)
	}
}
