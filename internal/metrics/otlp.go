package metrics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The OTLP/HTTP wire format a sandbox's metrics export (push.go) sends:
// JSON, which the protocol defines alongside protobuf. JSON is what the
// standard library can write; protobuf would need a dependency (AGENTS.md).

func encodeOTLP(resource []otlpKV, ms []*otlpMetric) ([]byte, error) {
	if resource == nil {
		resource = []otlpKV{}
	}
	return json.Marshal(otlpRequest{ResourceMetrics: []otlpResourceMetrics{{
		Resource:     otlpResource{Attributes: resource},
		ScopeMetrics: []otlpScopeMetrics{{Scope: otlpScope{Name: "sandbox-cli"}, Metrics: ms}},
	}}})
}

// postOTLP sends one gzipped request. Only the status of a refusal is
// returned: the body is the endpoint's to say and is not repeated into a
// log or an API response.
func postOTLP(ctx context.Context, client *http.Client, url string, headers http.Header, body []byte) error {
	var zb bytes.Buffer
	zw := gzip.NewWriter(&zb)
	zw.Write(body)
	if err := zw.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &zb)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s", resp.Status)
	}
	return nil
}

// exporterHeader is a header the push sets, or the transport does,
// which a configured header may not replace.
func exporterHeader(name string) bool {
	switch strings.ToLower(name) {
	case "host", "content-type", "content-encoding", "content-length", "transfer-encoding", "connection", "te", "trailer", "upgrade":
		return true
	}
	return false
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
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Unit        string     `json:"unit,omitempty"`
	Gauge       *otlpGauge `json:"gauge,omitempty"`
	Sum         *otlpSum   `json:"sum,omitempty"`
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
