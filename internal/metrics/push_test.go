package metrics

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestCheckClientEndpoint(t *testing.T) {
	for _, bad := range []string{
		"http://collector.example:4318", // plain http, anywhere
		"https://127.0.0.1:4318",        // loopback
		"https://localhost:4318",
		"https://169.254.169.254/latest", // a metadata address
		"https://10.0.0.5/v1/metrics",
		"https://[fd00::1]/v1/metrics",
		"https://u:p@collector.example",
		"https://collector.example/v1/metrics?key=s3cret",
		"https://collector.example/#x",
		"ftp://collector.example",
		"collector.example",
		"https://collector.example/" + strings.Repeat("a", MaxEndpointLen),
	} {
		if _, err := CheckClientEndpoint(bad, false); err == nil {
			t.Errorf("%q accepted", bad)
		} else if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "u:p") {
			t.Errorf("%q: the error repeats a credential: %v", bad, err)
		}
	}
	for in, want := range map[string]string{
		"https://collector.example":                      "https://collector.example/v1/metrics",
		"https://otlp.collector.example/otlp/v1/metrics": "https://otlp.collector.example/otlp/v1/metrics",
	} {
		if got, err := CheckClientEndpoint(in, false); err != nil || got != want {
			t.Errorf("%s: %q, %v", in, got, err)
		}
	}
	// The operator's flag allows private addresses, and http to loopback;
	// plain http elsewhere still not.
	for _, ok := range []string{"http://127.0.0.1:4318", "https://10.0.0.5", "http://localhost:4318"} {
		if _, err := CheckClientEndpoint(ok, true); err != nil {
			t.Errorf("allow-private %s: %v", ok, err)
		}
	}
	if _, err := CheckClientEndpoint("http://10.0.0.5:4318", true); err == nil {
		t.Error("plain http off loopback with allow-private")
	}
}

func TestCheckClientHeaders(t *testing.T) {
	if err := CheckClientHeaders(map[string]string{"Authorization": "Bearer x", "X-Scope-OrgID": "a"}); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]map[string]string{
		"bad name": {"Bad Name": "s3cret"},
		"host":     {"Host": "s3cret"},
		"encoding": {"content-encoding": "s3cret"},
		"empty":    {"Authorization": ""},
		"newline":  {"Authorization": "Bearer s3cret\r\nX-Evil: 1"},
		"twice":    {"authorization": "s3cret", "Authorization": "s3cret"},
		"long":     {"Authorization": strings.Repeat("s3cret", 1000)},
	} {
		err := CheckClientHeaders(h)
		if err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the error repeats the value: %v", name, err)
		}
	}
	many := map[string]string{}
	for i := 0; i <= MaxHeaders; i++ {
		many["X-H"+strings.Repeat("a", i+1)] = "v"
	}
	if CheckClientHeaders(many) == nil {
		t.Error("too many headers accepted")
	}
}

// The pusher's dial refuses loopback, so a client's endpoint that resolves
// there is refused even if its URL passed (a name that rebinds).
func TestClientPusherRefusesLoopback(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	p := Push{URL: srv.URL + "/v1/metrics", Time: time.Now(), Points: []Point{{Name: "x", Value: 1}}}
	err := p.Send(context.Background(), NewClientPusher(false))
	if err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Fatalf("push to loopback: %v", err)
	}
	if strings.Contains(err.Error(), srv.URL) {
		t.Errorf("the error repeats the URL: %v", err)
	}
	if c.count() != 0 {
		t.Fatal("the push reached loopback")
	}
	if err := p.Send(context.Background(), NewClientPusher(true)); err != nil {
		t.Fatalf("with allow-private: %v", err)
	}
}

func TestClientPusherDoesNotFollowRedirects(t *testing.T) {
	elsewhere := &collector{}
	other := httptest.NewServer(elsewhere)
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	p := Push{URL: srv.URL, Time: time.Now(), Points: []Point{{Name: "x", Value: 1}}}
	if err := p.Send(context.Background(), NewClientPusher(true)); err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("redirect: %v", err)
	}
	if elsewhere.count() != 0 {
		t.Error("the redirect was followed")
	}
}

func TestPushEncoding(t *testing.T) {
	c := &collector{}
	srv := httptest.NewServer(c)
	defer srv.Close()
	start := time.Unix(1000, 0)
	p := Push{
		URL:      srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer k"},
		Resource: []string{"sandbox.id", "sbx_1"},
		Start:    start, Time: start.Add(time.Minute),
		Points: []Point{
			{Name: "sandbox.cpu.utilization", Unit: "1", Value: 0.5},
			{Name: "sandbox.network.io", Unit: "By", Sum: true, Value: 10, Attrs: []string{"network.io.direction", "receive"}},
			{Name: "sandbox.network.io", Unit: "By", Sum: true, Value: 20, Attrs: []string{"network.io.direction", "transmit"}},
		},
	}
	if err := p.Send(context.Background(), NewClientPusher(true)); err != nil {
		t.Fatal(err)
	}
	if c.headers[0].Get("Authorization") != "Bearer k" {
		t.Errorf("headers %v", c.headers[0])
	}
	ms, res := metricsOf(t, c.bodies[0])
	if b, _ := json.Marshal(res); string(b) != `[{"key":"sandbox.id","value":{"stringValue":"sbx_1"}}]` {
		t.Errorf("resource %s", b)
	}
	if ms["sandbox.cpu.utilization"]["unit"] != "1" || ms["sandbox.cpu.utilization"]["gauge"] == nil {
		t.Errorf("cpu %v", ms["sandbox.cpu.utilization"])
	}
	sum := ms["sandbox.network.io"]["sum"].(map[string]any)
	pts := sum["dataPoints"].([]any)
	if len(pts) != 2 || pts[0].(map[string]any)["startTimeUnixNano"] != "1000000000000" || sum["isMonotonic"] != true {
		t.Errorf("network %v", sum)
	}
}
