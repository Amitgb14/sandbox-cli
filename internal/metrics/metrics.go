// Package metrics writes the Prometheus text exposition format by hand, for
// sandboxd's and sandbox-gateway's GET /metrics.
//
// It is the standard library and nothing else: the format is a few lines of
// text, and a client library would be the repository's first dependency
// that is not cobra or yaml (AGENTS.md). It holds only what the two servers
// need — counters and gauges with labels, and a histogram.
//
// Label values are the caller's responsibility to keep bounded: an enum the
// server defines, a route pattern, a node name the operator configured.
// Never a sandbox name, a user, a path or anything else a request chose —
// that is a series per request, and a way for a client to write into the
// operator's monitoring. Values are escaped here all the same, so a value
// that slipped through cannot break the exposition or forge a sample.
package metrics

import (
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ContentType is the exposition format's media type.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Writer writes one exposition. The first error sticks and is returned by Err.
type Writer struct {
	w    io.Writer
	err  error
	seen map[string]bool
}

// NewWriter returns a Writer on w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w, seen: map[string]bool{}} }

// Err is the first write error.
func (w *Writer) Err() error { return w.err }

func (w *Writer) printf(format string, a ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, a...)
	}
}

// Family writes a metric's HELP and TYPE lines, once per name.
func (w *Writer) Family(name, typ, help string) {
	if w.seen[name] {
		return
	}
	w.seen[name] = true
	w.printf("# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, typ)
}

// Sample writes one sample. labels are name, value pairs.
func (w *Writer) Sample(name string, value float64, labels ...string) {
	w.printf("%s%s %s\n", name, labelString(labels), formatValue(value))
}

// Gauge writes a family with one unlabelled sample.
func (w *Writer) Gauge(name, help string, value float64) {
	w.Family(name, "gauge", help)
	w.Sample(name, value)
}

func labelString(labels []string) string {
	if len(labels) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i+1 < len(labels); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(labels[i])
		b.WriteString(`="`)
		b.WriteString(EscapeLabel(labels[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// EscapeLabel escapes a label value as the format requires: backslash,
// double quote and newline.
func EscapeLabel(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

func escapeHelp(v string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v)
}

func formatValue(v float64) string {
	switch {
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// CounterVec is a counter with labels, safe for concurrent use.
type CounterVec struct {
	labels []string
	mu     sync.Mutex
	vals   map[string]float64
	keys   map[string][]string
}

// NewCounterVec returns a counter labelled by labelNames (none is a plain
// counter).
func NewCounterVec(labelNames ...string) *CounterVec {
	return &CounterVec{labels: labelNames, vals: map[string]float64{}, keys: map[string][]string{}}
}

// Add adds d to the series named by values, one per label name.
func (c *CounterVec) Add(d float64, values ...string) {
	if len(values) != len(c.labels) {
		panic("metrics: wrong number of label values")
	}
	k := strings.Join(values, "\x00")
	c.mu.Lock()
	if _, ok := c.keys[k]; !ok {
		c.keys[k] = append([]string(nil), values...)
	}
	c.vals[k] += d
	c.mu.Unlock()
}

// Inc adds one.
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

// Value is one series' value (tests).
func (c *CounterVec) Value(values ...string) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.vals[strings.Join(values, "\x00")]
}

// Write writes the family, its series sorted by label values. A counter
// with no labels writes 0 before its first Add, so a scraper sees it exist.
func (c *CounterVec) Write(w *Writer, name, help string) {
	w.Family(name, "counter", help)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.labels) == 0 && len(c.vals) == 0 {
		w.Sample(name, 0)
		return
	}
	ks := make([]string, 0, len(c.vals))
	for k := range c.vals {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		var lv []string
		for i, l := range c.labels {
			lv = append(lv, l, c.keys[k][i])
		}
		w.Sample(name, c.vals[k], lv...)
	}
}

// Histogram counts observations into cumulative buckets.
type Histogram struct {
	bounds []float64
	mu     sync.Mutex
	counts []uint64
	sum    float64
	n      uint64
}

// NewHistogram returns a histogram with the given upper bounds, ascending;
// +Inf is implied.
func NewHistogram(bounds ...float64) *Histogram {
	b := append([]float64(nil), bounds...)
	sort.Float64s(b)
	return &Histogram{bounds: b, counts: make([]uint64, len(b))}
}

// LatencyBuckets suit a create: from a warm pool's tens of milliseconds to
// an image pulled and built over a minute or more.
var LatencyBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}

// Observe records one value.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range h.bounds {
		if v <= b {
			h.counts[i]++
		}
	}
	h.sum += v
	h.n++
}

// Count is how many values were observed (tests).
func (h *Histogram) Count() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

// Write writes the histogram's family.
func (h *Histogram) Write(w *Writer, name, help string) {
	w.Family(name, "histogram", help)
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, b := range h.bounds {
		w.Sample(name+"_bucket", float64(h.counts[i]), "le", formatValue(b))
	}
	w.Sample(name+"_bucket", float64(h.n), "le", "+Inf")
	w.Sample(name+"_sum", h.sum)
	w.Sample(name+"_count", float64(h.n))
}

// Handler serves GET /metrics from write. It answers only a Host that is a
// loopback address or localhost: the listener is loopback only and needs no
// credential, and a web page that rebinds a name of its own to 127.0.0.1
// must not be able to read it.
func Handler(write func(*Writer)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		write(NewWriter(w))
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Listen opens a metrics listener, refusing any address but loopback: the
// endpoint has no credential, and what it says — node names, counts, how
// busy the machine is — is for the operator. A scraper elsewhere reaches it
// through a proxy the operator puts in front, with its own authentication.
func Listen(addr string) (net.Listener, error) {
	if err := CheckAddr(addr); err != nil {
		return nil, err
	}
	return net.Listen("tcp", addr)
}

// CheckAddr is Listen's refusal without the listening, for a command to
// check its flags before it starts anything.
func CheckAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--metrics-listen %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("--metrics-listen %s is not a loopback address; metrics are served without a credential, so only on this machine", addr)
	}
	return nil
}
