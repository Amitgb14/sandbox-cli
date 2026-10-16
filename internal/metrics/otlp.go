package metrics

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// OTLP export: the same families GET /metrics serves, pushed on an interval
// to an OpenTelemetry collector (or any endpoint that takes OTLP/HTTP) as
// JSON, which the protocol defines alongside protobuf. JSON is what the
// standard library can write; protobuf would need a dependency (AGENTS.md).
//
// Names are the Prometheus ones, so a dashboard built on a scrape reads the
// export too. Counters are cumulative monotonic sums starting when the
// exporter did, gauges are gauges, and a histogram is an explicit-bucket
// histogram with the same bounds.
//
// The push carries what the scrape does — node names, counts, how busy the
// machine is — off the machine, which the scrape refuses to do without a
// proxy the operator puts in front. So the endpoint is the operator's flag
// and nothing else's, plain HTTP is refused unless it stays on loopback
// (a collector agent on the same host), a credential it needs comes from a
// private file and never from the command line, and a redirect is not
// followed, so neither the series nor that credential go somewhere the
// operator did not name.

// OTLPConfig configures an Exporter.
type OTLPConfig struct {
	// Endpoint is the collector's URL. A URL with no path gets the
	// protocol's /v1/metrics.
	Endpoint string
	// Interval is how often to push; 60s when zero, the protocol's default.
	Interval time.Duration
	// HeadersFile holds "Name: value" lines sent with every push, for an
	// endpoint that wants a credential. It must be mode 0600.
	HeadersFile string
	// CAFile is a PEM bundle to verify an https endpoint with instead of
	// the system's roots.
	CAFile string
	// Service, Version and Instance are the resource's service.name,
	// service.version and service.instance.id.
	Service, Version, Instance string
	// Logf reports failed pushes, once per run of failures.
	Logf func(string, ...any)
}

// DefaultOTLPInterval is how often an Exporter pushes when not told.
const DefaultOTLPInterval = 60 * time.Second

// Exporter pushes a server's metrics to an OTLP/HTTP endpoint.
type Exporter struct {
	url      string
	interval time.Duration
	headers  http.Header
	client   *http.Client
	resource []otlpKV
	write    func(*Writer)
	logf     func(string, ...any)
	start    time.Time
	failing  bool
}

// CheckOTLPEndpoint returns the URL an endpoint flag names, or why it is
// refused: not http or https, carrying a user, a password or a query (which
// would sit in the process's argv; use a headers file), or plain HTTP off
// loopback.
func CheckOTLPEndpoint(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("--otlp-endpoint: %w", err)
	}
	// A user, a password or a query (where some services take a key) would
	// sit in the process's arguments and in every transport error logged.
	// Checked first, as the refusals below repeat the URL.
	if u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("--otlp-endpoint carries a user, a password or a query; put a credential in --otlp-headers-file, which is not in the process's arguments")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("--otlp-endpoint %q: want an http:// or https:// URL", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("--otlp-endpoint %q: no host", raw)
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", fmt.Errorf("--otlp-endpoint %q: a URL with a fragment", raw)
	}
	if u.Scheme == "http" && !loopbackHost(u.Host) {
		return "", fmt.Errorf("--otlp-endpoint %s is plain HTTP off this machine; metrics leave it only over https", raw)
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/metrics"
	}
	return u.String(), nil
}

// NewExporter checks cfg and returns an Exporter pushing what write writes.
// Nothing is sent until Run.
func NewExporter(cfg OTLPConfig, write func(*Writer)) (*Exporter, error) {
	endpoint, err := CheckOTLPEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	interval := cfg.Interval
	if interval == 0 {
		interval = DefaultOTLPInterval
	}
	if interval < time.Second {
		return nil, fmt.Errorf("--otlp-interval %v: at least 1s", interval)
	}
	headers := http.Header{}
	if cfg.HeadersFile != "" {
		if headers, err = readOTLPHeaders(cfg.HeadersFile); err != nil {
			return nil, err
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("--otlp-ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--otlp-ca %s: no certificate in it", cfg.CAFile)
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	logf := cfg.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	var res []otlpKV
	for _, kv := range [][2]string{{"service.name", cfg.Service}, {"service.version", cfg.Version}, {"service.instance.id", cfg.Instance}} {
		if kv[1] != "" {
			res = append(res, strAttr(kv[0], kv[1]))
		}
	}
	return &Exporter{
		url:      endpoint,
		interval: interval,
		headers:  headers,
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		resource: res,
		write:    write,
		logf:     logf,
		start:    time.Now(),
	}, nil
}

// URL is where the exporter pushes.
func (e *Exporter) URL() string { return e.url }

// Interval is how often it pushes.
func (e *Exporter) Interval() time.Duration { return e.interval }

// Run pushes every interval until ctx ends, then once more, so what
// happened since the last push is not lost to a restart.
func (e *Exporter) Run(ctx context.Context) {
	t := time.NewTicker(e.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			final, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e.report(e.Export(final))
			cancel()
			return
		case <-t.C:
			push, cancel := context.WithTimeout(ctx, min(e.interval, 10*time.Second))
			e.report(e.Export(push))
			cancel()
		}
	}
}

// Start runs the exporter in the background. stop ends it, waits for the
// last push, and may be called more than once: a command calls it before
// closing what the metrics read from, and defers it for the early returns.
func (e *Exporter) Start(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}

// report logs the first failure of a run of them and the recovery, not
// every push: a collector down for a day is one line, not 1440.
func (e *Exporter) report(err error) {
	switch {
	case err != nil && !e.failing:
		e.failing = true
		e.logf("otlp: pushing metrics to %s: %v; retrying every %v", e.url, err, e.interval)
	case err == nil && e.failing:
		e.failing = false
		e.logf("otlp: pushing metrics to %s again", e.url)
	}
}

// Export pushes one snapshot now.
func (e *Exporter) Export(ctx context.Context) error {
	body, err := e.encode(time.Now())
	if err != nil {
		return err
	}
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	zw.Write(body)
	if err := zw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, &zb)
	if err != nil {
		return err
	}
	for k, v := range e.headers {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		// The body is the endpoint's to say and is not repeated into the
		// log; the status is enough to go and look.
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

// encode is one ExportMetricsServiceRequest, as JSON.
func (e *Exporter) encode(now time.Time) ([]byte, error) {
	s := &snapshot{byName: map[string]*otlpMetric{}, start: uint64(e.start.UnixNano()), now: uint64(now.UnixNano())}
	w := &Writer{seen: map[string]bool{}, snap: s}
	e.write(w)
	ms := make([]*otlpMetric, 0, len(s.metrics))
	for _, m := range s.metrics {
		if !m.empty() {
			ms = append(ms, m)
		}
	}
	return json.Marshal(otlpRequest{ResourceMetrics: []otlpResourceMetrics{{
		Resource:     otlpResource{Attributes: e.resource},
		ScopeMetrics: []otlpScopeMetrics{{Scope: otlpScope{Name: "sandbox-cli"}, Metrics: ms}},
	}}})
}

// readOTLPHeaders reads "Name: value" lines; blank lines and # comments are
// skipped. The file holds a credential, so it must be private, and nothing
// in it is ever logged.
func readOTLPHeaders(path string) (http.Header, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("--otlp-headers-file: %w", err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("--otlp-headers-file %s is readable by others (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("--otlp-headers-file: %w", err)
	}
	defer f.Close()
	h := http.Header{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		// The line number, never the line: it may be the credential.
		if !ok || !validHeaderName(name) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, fmt.Errorf("--otlp-headers-file %s, line %d: want Name: value", path, n)
		}
		switch strings.ToLower(name) {
		case "host", "content-type", "content-encoding", "content-length", "transfer-encoding", "connection":
			return nil, fmt.Errorf("--otlp-headers-file %s, line %d: %s is the exporter's to set", path, n, name)
		}
		h.Add(name, value)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("--otlp-headers-file: %w", err)
	}
	return h, nil
}

// validHeaderName is RFC 9110's token.
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.ContainsRune("!#$%&'*+-.^_`|~", c):
		default:
			return false
		}
	}
	return true
}

// snapshot is what a Writer records instead of text.
type snapshot struct {
	metrics    []*otlpMetric
	byName     map[string]*otlpMetric
	start, now uint64
}

func (s *snapshot) family(name, typ, help string) {
	m := &otlpMetric{Name: name, Description: help}
	switch typ {
	case "counter":
		m.Sum = &otlpSum{Temporality: temporalityCumulative, Monotonic: true}
	case "histogram":
		m.Histogram = &otlpHistogram{Temporality: temporalityCumulative}
	default:
		m.Gauge = &otlpGauge{}
	}
	s.metrics = append(s.metrics, m)
	s.byName[name] = m
}

func (s *snapshot) sample(name string, v float64, labels []string) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return // JSON has no way to say it
	}
	m := s.byName[name]
	if m == nil {
		s.family(name, "gauge", "")
		m = s.byName[name]
	}
	p := otlpNumberPoint{Attributes: attrs(labels), Time: s.now, Value: v}
	switch {
	case m.Sum != nil:
		p.Start = s.start
		m.Sum.Points = append(m.Sum.Points, p)
	case m.Gauge != nil:
		m.Gauge.Points = append(m.Gauge.Points, p)
	}
}

// histogram turns cumulative bucket counts into OTLP's per-bucket ones,
// with the +Inf bucket last.
func (s *snapshot) histogram(name string, bounds []float64, cum []uint64, n uint64, sum float64) {
	m := s.byName[name]
	if m == nil || m.Histogram == nil {
		return
	}
	counts := make([]string, len(bounds)+1)
	var prev uint64
	for i, c := range cum {
		counts[i] = fmt.Sprint(c - prev)
		prev = c
	}
	counts[len(bounds)] = fmt.Sprint(n - prev)
	m.Histogram.Points = append(m.Histogram.Points, otlpHistogramPoint{
		Attributes: []otlpKV{}, Start: s.start, Time: s.now,
		Count: n, Sum: sum, BucketCounts: counts, Bounds: append([]float64{}, bounds...),
	})
}

func attrs(labels []string) []otlpKV {
	out := []otlpKV{}
	for i := 0; i+1 < len(labels); i += 2 {
		out = append(out, strAttr(labels[i], labels[i+1]))
	}
	return out
}

func strAttr(k, v string) otlpKV { return otlpKV{Key: k, Value: otlpValue{String: v}} }

// The OTLP JSON encoding (opentelemetry-proto, metrics/v1): field names in
// lowerCamelCase, 64-bit integers as decimal strings, enums as numbers.

const temporalityCumulative = 2

type otlpRequest struct {
	ResourceMetrics []otlpResourceMetrics `json:"resourceMetrics"`
}

type otlpResourceMetrics struct {
	Resource     otlpResource       `json:"resource"`
	ScopeMetrics []otlpScopeMetrics `json:"scopeMetrics"`
}

type otlpResource struct {
	Attributes []otlpKV `json:"attributes"`
}

type otlpScopeMetrics struct {
	Scope   otlpScope     `json:"scope"`
	Metrics []*otlpMetric `json:"metrics"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpKV struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	String string `json:"stringValue"`
}

type otlpMetric struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Gauge       *otlpGauge     `json:"gauge,omitempty"`
	Sum         *otlpSum       `json:"sum,omitempty"`
	Histogram   *otlpHistogram `json:"histogram,omitempty"`
}

// empty is a family that wrote no sample: a labelled counter nothing has
// counted yet, a node list with no nodes. OTLP has no family without points.
func (m *otlpMetric) empty() bool {
	switch {
	case m.Gauge != nil:
		return len(m.Gauge.Points) == 0
	case m.Sum != nil:
		return len(m.Sum.Points) == 0
	case m.Histogram != nil:
		return len(m.Histogram.Points) == 0
	}
	return true
}

type otlpGauge struct {
	Points []otlpNumberPoint `json:"dataPoints"`
}

type otlpSum struct {
	Points      []otlpNumberPoint `json:"dataPoints"`
	Temporality int               `json:"aggregationTemporality"`
	Monotonic   bool              `json:"isMonotonic"`
}

type otlpNumberPoint struct {
	Attributes []otlpKV `json:"attributes"`
	Start      uint64   `json:"startTimeUnixNano,string,omitempty"`
	Time       uint64   `json:"timeUnixNano,string"`
	Value      float64  `json:"asDouble"`
}

type otlpHistogram struct {
	Points      []otlpHistogramPoint `json:"dataPoints"`
	Temporality int                  `json:"aggregationTemporality"`
}

type otlpHistogramPoint struct {
	Attributes   []otlpKV  `json:"attributes"`
	Start        uint64    `json:"startTimeUnixNano,string"`
	Time         uint64    `json:"timeUnixNano,string"`
	Count        uint64    `json:"count,string"`
	Sum          float64   `json:"sum"`
	BucketCounts []string  `json:"bucketCounts"`
	Bounds       []float64 `json:"explicitBounds"`
}
