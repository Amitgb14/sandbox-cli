package spec

import (
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// DefaultMinMetricsExportSecs is limits.min_metrics_export_secs when the
// policy says nothing: every push is a connection the host makes, per
// sandbox, to somewhere a client chose.
const DefaultMinMetricsExportSecs = 10

// DefaultMetricsExportSecs is a metrics export's interval when the request
// gives none: OTLP's own default.
const DefaultMetricsExportSecs = 60

// MetricsExport is a resolved metrics export: where, how often, and the
// header values, which only the server holds.
type MetricsExport struct {
	URL          string
	IntervalSecs int
	Headers      map[string]string
}

// Status is the export as GET shows it: header names, never values.
func (m *MetricsExport) Status() *api.MetricsExportStatus {
	if m == nil {
		return nil
	}
	st := &api.MetricsExportStatus{Endpoint: m.URL, IntervalSecs: m.IntervalSecs}
	for name := range m.Headers {
		st.HeaderNames = append(st.HeaderNames, name)
	}
	sort.Strings(st.HeaderNames)
	return st
}

// ResolveMetricsExport checks a request's metrics export against the
// server's limits. nil, or an empty endpoint, is none. allowPrivate is the
// operator's flag (sandboxd --metrics-export-allow-private). header_secrets
// is a gateway's to resolve; one that reaches a server is refused, since a
// header asked for and not sent would be a push the collector turns away.
func ResolveMetricsExport(req *api.MetricsExport, l api.Limits, allowPrivate bool) (*MetricsExport, error) {
	if req == nil || req.Endpoint == "" {
		if req != nil && (req.IntervalSecs != 0 || len(req.Headers) > 0 || len(req.HeaderSecrets) > 0) {
			return nil, invalid("metrics_export: an interval or headers without an endpoint")
		}
		return nil, nil
	}
	if len(req.HeaderSecrets) > 0 {
		return nil, &Refused{"metrics_export: header_secrets name a gateway's secrets, and this server has none; send headers"}
	}
	url, err := metrics.CheckClientEndpoint(req.Endpoint, allowPrivate)
	if err != nil {
		return nil, invalid("metrics_export: %v", err)
	}
	if err := metrics.CheckClientHeaders(req.Headers); err != nil {
		return nil, invalid("metrics_export: %v", err)
	}
	min := l.MinMetricsExportSecs
	if min < 1 {
		min = DefaultMinMetricsExportSecs
	}
	every := req.IntervalSecs
	switch {
	case every < 0:
		return nil, invalid("metrics_export: interval_secs must not be negative")
	case every == 0:
		every = max(DefaultMetricsExportSecs, min)
	case every < min:
		return nil, invalid("metrics_export: interval_secs %d: at least %d", every, min)
	}
	m := &MetricsExport{URL: url, IntervalSecs: every}
	if len(req.Headers) > 0 {
		m.Headers = map[string]string{}
		for k, v := range req.Headers {
			m.Headers[canonicalHeader(k)] = v
		}
	}
	return m, nil
}

func canonicalHeader(name string) string { return http.CanonicalHeaderKey(name) }
