package server

import (
	"context"
	"net/http"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// A sandbox's usage over the last hour, sampled here from what the backend
// reads on the host — the runtime's own counters on macOS, the VMM's process
// and tap device on Linux — so nothing in it is the guest's account of
// itself. One reading per interval for every running sandbox; an hour of
// samples kept with each, in memory, gone with it.

// defaultMetricsInterval is how often usage is sampled when the server's
// MetricsInterval is unset: an hour is 360 samples.
const defaultMetricsInterval = 10 * time.Second

const metricsWindow = time.Hour

// metricsState is a record's samples and what the next CPU share is measured
// from. Under the record's mutex.
type metricsState struct {
	samples []api.MetricSample
	prevCPU int64
	prevAt  time.Time
}

func (s *Server) metricsInterval() time.Duration {
	if s.MetricsInterval > 0 {
		return s.MetricsInterval
	}
	return defaultMetricsInterval
}

// usageReader is the backend's, when it can read usage at all.
func (s *Server) usageReader() (backend.UsageReader, bool) {
	ur, ok := s.Backend.(backend.UsageReader)
	return ur, ok
}

// runMetrics samples every interval until the process ends.
func (s *Server) runMetrics() {
	if _, ok := s.usageReader(); !ok {
		return
	}
	t := time.NewTicker(s.metricsInterval())
	defer t.Stop()
	for range t.C {
		s.sampleMetrics(context.Background())
	}
}

// sampleMetrics takes one reading of every running sandbox.
func (s *Server) sampleMetrics(ctx context.Context) {
	ur, ok := s.usageReader()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.metricsInterval())
	defer cancel()
	usage, err := ur.Usage(ctx)
	if err != nil {
		s.logf("reading usage: %v", err)
		return
	}
	now := s.now()
	s.mu.Lock()
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	s.mu.Unlock()
	for _, rec := range recs {
		rec.mu.Lock()
		u, ok := usage[rec.sbx.ID]
		if ok && rec.sbx.State == api.StateRunning {
			rec.metrics.add(now, u, rec.sbx.CPUs)
		}
		rec.mu.Unlock()
	}
}

// add records one reading. The CPU share is the CPU time used since the last
// reading over the time it covers and the vCPUs the sandbox was given; the
// first reading has nothing to measure from, so it records 0.
func (m *metricsState) add(now time.Time, u backend.Usage, cpus float64) {
	pct := 0.0
	if !m.prevAt.IsZero() && cpus > 0 {
		span := now.Sub(m.prevAt).Microseconds()
		if used := u.CPUUsec - m.prevCPU; span > 0 && used >= 0 {
			pct = float64(used) / (float64(span) * cpus) * 100
		}
	}
	pct = min(max(pct, 0), 100)
	m.prevCPU, m.prevAt = u.CPUUsec, now
	m.samples = append(m.samples, api.MetricSample{
		Time: now.UTC(), CPUPercent: pct,
		MemoryBytes: u.MemoryBytes, MemoryLimitBytes: u.MemoryLimitBytes,
		NetRxBytes: u.NetRxBytes, NetTxBytes: u.NetTxBytes,
		DiskReadBytes: u.DiskReadBytes, DiskWriteBytes: u.DiskWriteBytes,
		Processes: u.Processes,
	})
	cut := 0
	for cut < len(m.samples) && now.Sub(m.samples[cut].Time) > metricsWindow {
		cut++
	}
	m.samples = m.samples[cut:]
}

// sandboxMetrics serves GET /v1/sandboxes/{ref}/metrics: the last hour, oldest first.
func (s *Server) sandboxMetrics(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.usageReader(); !ok {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint does not measure sandboxes' usage")
		return
	}
	// find, not lookup: a dashboard reading usage every few seconds is not
	// the sandbox being used, and must not keep it from idling out.
	rec, ok := s.find(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	rec.mu.Lock()
	out := api.MetricsList{IntervalSecs: int(s.metricsInterval() / time.Second), Samples: append([]api.MetricSample{}, rec.metrics.samples...)}
	rec.mu.Unlock()
	if out.IntervalSecs == 0 {
		out.IntervalSecs = 1
	}
	writeJSON(w, http.StatusOK, out)
}
