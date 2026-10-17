package metrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/publicnet"
)

// A sandbox's own metrics, pushed to an endpoint its owner named
// (CreateSandboxRequest.MetricsExport), as OTLP/HTTP (otlp.go). The URL
// and its headers come from an API request, so they are untrusted input:
//
//   - The host connects to the URL from wherever it runs, beside its own
//     loopback services, the nodes' network and a cloud's metadata address.
//     So it is https to a public address, checked as it is dialled
//     (publicnet), unless the operator allows private addresses — and then
//     http to loopback as well, for a collector on the same machine.
//   - No proxy (it would dial on the client's behalf, unchecked) and no
//     redirect (a public endpoint could point the push anywhere).
//   - Header values are credentials: never logged, never in an error, never
//     returned. A refusal names the header, never its value.
//   - A failed push is the sandbox's status, not a line in the operator's
//     log: an endpoint a client chose must not be a way to write there.

// MaxEndpointLen bounds a client's endpoint URL.
const MaxEndpointLen = 2048

// Bounds on a client's headers.
const (
	MaxHeaders        = 16
	MaxHeaderValueLen = 4096
)

// CheckClientEndpoint returns the URL a client's endpoint names, or why it
// is refused. allowPrivate is the operator's flag.
func CheckClientEndpoint(raw string, allowPrivate bool) (string, error) {
	if len(raw) > MaxEndpointLen {
		return "", fmt.Errorf("endpoint: at most %d bytes", MaxEndpointLen)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", errors.New("endpoint: not an absolute URL")
	}
	// A credential belongs in a header, which is never returned; the
	// endpoint is shown back with the sandbox and recorded in its events.
	if u.User != nil || u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("endpoint: a URL with a user, a password or a query is refused; send a credential as a header")
	}
	if u.Fragment != "" || u.RawFragment != "" {
		return "", errors.New("endpoint: a URL with a fragment is refused")
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate || !loopbackHost(u.Host) {
			return "", errors.New("endpoint: want an https URL")
		}
	default:
		return "", errors.New("endpoint: want an https URL")
	}
	if !allowPrivate {
		if ip, err := netip.ParseAddr(h); h == "localhost" || strings.HasSuffix(h, ".localhost") || (err == nil && !publicnet.Addr(ip)) {
			return "", fmt.Errorf("endpoint: %s is a loopback, private or link-local address, which this server does not push to", h)
		}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/metrics"
	}
	return u.String(), nil
}

// CheckClientHeaders refuses headers that are not valid, too many or too
// long, or that the push sets itself. Errors name a header, never a value.
func CheckClientHeaders(h map[string]string) error {
	if len(h) > MaxHeaders {
		return fmt.Errorf("headers: at most %d", MaxHeaders)
	}
	seen := map[string]bool{}
	for name, value := range h {
		if !validHeaderName(name) {
			return fmt.Errorf("headers: %q is not a header name", name)
		}
		if exporterHeader(name) {
			return fmt.Errorf("headers: %s is the push's to set", name)
		}
		canon := http.CanonicalHeaderKey(name)
		if seen[canon] {
			return fmt.Errorf("headers: %s given twice", canon)
		}
		seen[canon] = true
		if value == "" || len(value) > MaxHeaderValueLen || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("headers: %s's value is empty, longer than %d bytes or has a line break", canon, MaxHeaderValueLen)
		}
	}
	return nil
}

// NewClientPusher returns the HTTP client a server pushes clients' metrics
// with: public addresses only unless allowPrivate, no proxy, no redirect.
func NewClientPusher(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = publicnet.Control("metrics export", "sandboxd --metrics-export-allow-private")
	}
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second,
			MaxIdleConns: 16, MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second,
			ForceAttemptHTTP2: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Point is one value of a pushed series.
type Point struct {
	Name, Description, Unit string
	// Sum is a cumulative monotonic counter running from the push's Start;
	// otherwise a gauge.
	Sum   bool
	Value float64
	Attrs []string // name, value pairs
}

// Push is one OTLP request.
type Push struct {
	URL      string
	Headers  map[string]string
	Resource []string // name, value pairs
	// Start is when the counters started; Time is when the values were read.
	Start, Time time.Time
	Points      []Point
}

// Send encodes and posts the push. Points of one name are one metric, in
// the order first seen.
func (p Push) Send(ctx context.Context, client *http.Client) error {
	start, now := uint64(p.Start.UnixNano()), uint64(p.Time.UnixNano())
	var ms []*otlpMetric
	byName := map[string]*otlpMetric{}
	for _, pt := range p.Points {
		if math.IsNaN(pt.Value) || math.IsInf(pt.Value, 0) {
			continue
		}
		m := byName[pt.Name]
		if m == nil {
			m = &otlpMetric{Name: pt.Name, Description: pt.Description, Unit: pt.Unit}
			if pt.Sum {
				m.Sum = &otlpSum{Temporality: temporalityCumulative, Monotonic: true}
			} else {
				m.Gauge = &otlpGauge{}
			}
			byName[pt.Name] = m
			ms = append(ms, m)
		}
		np := otlpNumberPoint{Attributes: attrs(pt.Attrs), Time: now, Value: pt.Value}
		if m.Sum != nil {
			np.Start = start
			m.Sum.Points = append(m.Sum.Points, np)
		} else {
			m.Gauge.Points = append(m.Gauge.Points, np)
		}
	}
	body, err := encodeOTLP(attrs(p.Resource), ms)
	if err != nil {
		return err
	}
	h := http.Header{}
	for k, v := range p.Headers {
		h.Set(k, v)
	}
	if err := postOTLP(ctx, client, p.URL, h, body); err != nil {
		return pushError(err)
	}
	return nil
}

// pushError is what a failed push says in a sandbox's status: the status
// the endpoint answered, or what kind of failure the connection was. Go's
// transport errors repeat the URL and wrap the dialer's; the URL is the
// client's own and the reason is what they need, so it is kept short.
func pushError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return errors.New("timed out")
		}
		err = ue.Err
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return errors.New(msg)
}
