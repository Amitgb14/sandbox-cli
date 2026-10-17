package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A sandbox's metrics export: its owner names an OTLP/HTTP endpoint at
// create or in an update, and each newest usage sample (metrics_sampler.go)
// is pushed there every interval_secs. The values are the host's
// measurements, the same GET …/metrics serves; nothing the guest says of
// itself is sent, and the push is the host's, so the sandbox's own network
// policy does not decide it. What leaves is numbers, and the sandbox's id,
// name, image and labels: the owner's own, to the owner's collector.
//
// The endpoint and headers are a client's (metrics.CheckClientEndpoint):
// public addresses only unless the operator allows private ones
// (MetricsExportAllowPrivate), checked as each push is dialled. Header
// values are kept as environment values are — in memory, and in the
// record a kept sandbox is written to — and are never returned, logged or
// recorded in an event. A push that fails is the sandbox's status, never a
// line in the operator's log.

// maxConcurrentPushes bounds the pushes in flight at once across every
// sandbox, so many sandboxes with slow endpoints cannot hold the host's
// connections; a sandbox whose last push is still going skips its turn.
const maxConcurrentPushes = 32

// exportState is a record's export, under the record's mutex. The status
// GET shows is rec.sbx.MetricsExport.
type exportState struct {
	headers map[string]string
	// gen changes when the export is set or stopped, so a push that ends
	// after that does not write its result over the new export's status.
	gen      int
	inFlight bool
	// pushedAt is the time of the sample last sent, and triedAt when the
	// last push began.
	pushedAt, triedAt time.Time
}

// canExport reports whether this server can push sandboxes' usage: it must
// sample it.
func (s *Server) canExport() bool {
	_, ok := s.usageReader()
	return ok
}

// resolveExport checks a request's export, refusing it where this server
// cannot sample usage: an export asked for and never pushed is refused, not
// accepted and silent.
func (s *Server) resolveExport(w http.ResponseWriter, req *api.MetricsExport) (*spec.MetricsExport, bool) {
	m, err := spec.ResolveMetricsExport(req, s.Policy.Limits, s.MetricsExportAllowPrivate)
	if err != nil {
		writeSpecErr(w, err)
		return nil, false
	}
	if m != nil && !s.canExport() {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint does not measure sandboxes' usage, so it cannot export it")
		return nil, false
	}
	return m, true
}

// setExport replaces rec's export with m (nil stops it). Under rec.mu.
func (rec *record) setExport(m *spec.MetricsExport) {
	gen := rec.export.gen + 1
	rec.export = exportState{gen: gen}
	rec.sbx.MetricsExport = nil
	if m == nil {
		return
	}
	rec.export.headers = m.Headers
	rec.sbx.MetricsExport = m.Status()
	// The first push waits for a sample taken after this, so a sandbox set
	// to export does not send one read before it asked.
	if n := len(rec.metrics.samples); n > 0 {
		rec.export.pushedAt = rec.metrics.samples[n-1].Time
	}
}

func (s *Server) pusher() *http.Client {
	s.pushOnce.Do(func() {
		s.pushClient = metrics.NewClientPusher(s.MetricsExportAllowPrivate)
		s.pushSlots = make(chan struct{}, maxConcurrentPushes)
	})
	return s.pushClient
}

// pushExports starts a push for each running sandbox whose export is due
// and has a sample it has not sent. Called after each sampling.
func (s *Server) pushExports(now time.Time) {
	s.mu.Lock()
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	s.mu.Unlock()
	client := s.pusher()
	for _, rec := range recs {
		// A slot first: with every one busy, this sandbox's push waits for
		// the next sampling, its turn not spent.
		select {
		case s.pushSlots <- struct{}{}:
		default:
			return
		}
		p, gen, ok := rec.duePush(now)
		if !ok {
			<-s.pushSlots
			continue
		}
		go func() {
			defer func() { <-s.pushSlots }()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			err := p.Send(ctx, client)
			cancel()
			rec.pushed(gen, p.Time, s.now(), err)
		}()
	}
}

// duePush is rec's next push, if one is due, and marks it in flight.
func (rec *record) duePush(now time.Time) (metrics.Push, int, bool) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	st := rec.sbx.MetricsExport
	if st == nil || rec.export.inFlight || rec.sbx.State != api.StateRunning || len(rec.metrics.samples) == 0 {
		return metrics.Push{}, 0, false
	}
	sample := rec.metrics.samples[len(rec.metrics.samples)-1]
	if !sample.Time.After(rec.export.pushedAt) {
		return metrics.Push{}, 0, false
	}
	if !rec.export.triedAt.IsZero() && now.Sub(rec.export.triedAt) < time.Duration(st.IntervalSecs)*time.Second {
		return metrics.Push{}, 0, false
	}
	rec.export.inFlight, rec.export.triedAt = true, now
	headers := make(map[string]string, len(rec.export.headers))
	for k, v := range rec.export.headers {
		headers[k] = v
	}
	return metrics.Push{
		URL: st.Endpoint, Headers: headers,
		Resource: sandboxResource(rec.sbx),
		// The counters run from the sandbox's start (api.MetricSample).
		Start: rec.sbx.CreatedAt, Time: sample.Time,
		Points: samplePoints(sample),
	}, rec.export.gen, true
}

// pushed records how a push went in the sandbox's status.
func (rec *record) pushed(gen int, sampleAt, now time.Time, err error) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.export.gen != gen || rec.sbx.MetricsExport == nil {
		return
	}
	rec.export.inFlight = false
	st := *rec.sbx.MetricsExport
	if err == nil {
		rec.export.pushedAt = sampleAt
		at := now.UTC()
		st.LastPushAt, st.LastError, st.FailingSince = &at, "", nil
	} else {
		st.LastError = err.Error()
		if st.FailingSince == nil {
			at := now.UTC()
			st.FailingSince = &at
		}
	}
	rec.sbx.MetricsExport = &st
}

// sandboxResource names the sandbox a push is about: its id, and its name,
// image and labels — the owner's own. A gateway's labels (gateway.*) are
// its bookkeeping and are left out.
func sandboxResource(sbx api.Sandbox) []string {
	r := []string{"service.name", "sandbox", "sandbox.id", sbx.ID, "sandbox.image", sbx.Image}
	if sbx.Name != "" {
		r = append(r, "sandbox.name", sbx.Name)
	}
	keys := make([]string, 0, len(sbx.Labels))
	for k := range sbx.Labels {
		if !strings.HasPrefix(k, "gateway.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		r = append(r, "sandbox.label."+k, sbx.Labels[k])
	}
	return r
}

// samplePoints is one sample as OTLP series. Utilization is a ratio, as
// the semantic conventions have it; the byte counters are cumulative sums.
func samplePoints(m api.MetricSample) []metrics.Point {
	pts := []metrics.Point{
		{Name: "sandbox.cpu.utilization", Unit: "1", Description: "Share of the sandbox's vCPUs used since the previous sample, 0 to 1.", Value: m.CPUPercent / 100},
		{Name: "sandbox.memory.usage", Unit: "By", Description: "Memory the sandbox uses, as the host measures it.", Value: float64(m.MemoryBytes)},
	}
	if m.MemoryLimitBytes > 0 {
		pts = append(pts, metrics.Point{Name: "sandbox.memory.limit", Unit: "By", Description: "Memory the sandbox was given.", Value: float64(m.MemoryLimitBytes)})
	}
	const netHelp, diskHelp = "Bytes through the sandbox's network interface since it started.", "Bytes the sandbox read from and wrote to its disk since it started."
	pts = append(pts,
		metrics.Point{Name: "sandbox.network.io", Unit: "By", Sum: true, Description: netHelp, Value: float64(m.NetRxBytes), Attrs: []string{"network.io.direction", "receive"}},
		metrics.Point{Name: "sandbox.network.io", Unit: "By", Sum: true, Description: netHelp, Value: float64(m.NetTxBytes), Attrs: []string{"network.io.direction", "transmit"}},
		metrics.Point{Name: "sandbox.disk.io", Unit: "By", Sum: true, Description: diskHelp, Value: float64(m.DiskReadBytes), Attrs: []string{"disk.io.direction", "read"}},
		metrics.Point{Name: "sandbox.disk.io", Unit: "By", Sum: true, Description: diskHelp, Value: float64(m.DiskWriteBytes), Attrs: []string{"disk.io.direction", "write"}},
	)
	if m.Processes > 0 {
		pts = append(pts, metrics.Point{Name: "sandbox.processes", Unit: "{process}", Description: "Processes running in the sandbox.", Value: float64(m.Processes)})
	}
	return pts
}

// exportEndpoint is what an event records of an export: where, or "none".
func exportEndpoint(m *spec.MetricsExport) string {
	if m == nil {
		return "none"
	}
	return m.URL
}
