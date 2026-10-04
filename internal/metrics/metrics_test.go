package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExposition(t *testing.T) {
	c := NewCounterVec("route", "code")
	c.Inc("GET /v1/x", "200")
	c.Add(2, "GET /v1/x", "200")
	c.Inc("evil\"} 1\nforged 9", "500")
	plain := NewCounterVec()
	h := NewHistogram(1, 0.1)
	h.Observe(0.05)
	h.Observe(0.5)
	h.Observe(5)

	var b strings.Builder
	w := NewWriter(&b)
	c.Write(w, "reqs_total", "requests")
	plain.Write(w, "plain_total", "a counter\nwith a newline")
	h.Write(w, "lat_seconds", "latency")
	w.Gauge("up", "up", 1)
	w.Family("up", "gauge", "written twice") // no second HELP
	if w.Err() != nil {
		t.Fatal(w.Err())
	}
	got := b.String()
	for _, want := range []string{
		"# TYPE reqs_total counter\n",
		`reqs_total{route="GET /v1/x",code="200"} 3` + "\n",
		`reqs_total{route="evil\"} 1\nforged 9",code="500"} 1` + "\n",
		"# HELP plain_total a counter\\nwith a newline\n",
		"plain_total 0\n",
		`lat_seconds_bucket{le="0.1"} 1` + "\n",
		`lat_seconds_bucket{le="1"} 2` + "\n",
		`lat_seconds_bucket{le="+Inf"} 3` + "\n",
		"lat_seconds_sum 5.55\n",
		"lat_seconds_count 3\n",
		"up 1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Count(got, "# HELP up") != 1 {
		t.Errorf("HELP written twice:\n%s", got)
	}
	// Every line is a comment or a sample: an escaped value did not start a
	// line of its own.
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(line, "forged") {
			t.Errorf("a label value forged a sample: %q", line)
		}
	}
}

func TestHandlerRefusesOtherHosts(t *testing.T) {
	h := Handler(func(w *Writer) { w.Gauge("up", "up", 1) })
	for host, want := range map[string]int{
		"127.0.0.1:9100":     200,
		"localhost:9100":     200,
		"[::1]:9100":         200,
		"evil.example:9100":  403,
		"10.0.0.1:9100":      403,
		"127.0.0.1.nip.io:1": 403,
	} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("Host %s: %d, want %d", host, rec.Code, want)
		}
		if want == 200 && rec.Header().Get("Content-Type") != ContentType {
			t.Errorf("content type %q", rec.Header().Get("Content-Type"))
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/metrics", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestListenRefusesNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "10.1.2.3:9100", "example.com:9100", "nonsense"} {
		if ln, err := Listen(addr); err == nil {
			ln.Close()
			t.Errorf("Listen(%q) succeeded", addr)
		}
	}
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}
