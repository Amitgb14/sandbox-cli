package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// GET /metrics on a separate loopback listener (sandboxd --metrics-listen):
// what one node is doing, for the operator's monitoring. It is not on the
// API's listener, which every client reaches with the API's token; metrics
// need none, and so are served only on this machine (metrics.Listen).
//
// Every label value is bounded: a sandbox state or a status code the server
// defines, or a pool's image, which is the operator's policy.

type nodeMetrics struct {
	creates   *metrics.CounterVec // by status code
	createLat *metrics.Histogram  // successful creates
}

func (s *Server) metrics() *nodeMetrics {
	s.metricsOnce.Do(func() {
		s.nm = &nodeMetrics{
			creates:   metrics.NewCounterVec("code"),
			createLat: metrics.NewHistogram(metrics.LatencyBuckets...),
		}
	})
	return s.nm
}

// timedCreate is createSandbox, counted by its status and timed when it
// succeeds.
func (s *Server) timedCreate(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w}
	s.createSandbox(sw, r)
	m := s.metrics()
	code := sw.code
	if code == 0 {
		code = http.StatusOK
	}
	m.creates.Inc(strconv.Itoa(code))
	if code == http.StatusCreated {
		m.createLat.Observe(time.Since(start).Seconds())
	}
}

// statusWriter remembers the status written. Create writes JSON and
// nothing else, so it needs no Flush or Hijack.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// MetricsHandler serves GET /metrics in the Prometheus text format.
func (s *Server) MetricsHandler() http.Handler {
	return metrics.Handler(s.writeMetrics)
}

func (s *Server) writeMetrics(w *metrics.Writer) {
	st := s.nodeStatus()
	s.mu.Lock()
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	pools := s.pools
	s.mu.Unlock()

	states := map[string]int{api.StatePending: 0, api.StateRunning: 0, api.StateSuspended: 0, api.StateTerminated: 0}
	procs := 0
	for _, rec := range recs {
		rec.mu.Lock()
		states[rec.sbx.State]++
		for _, p := range rec.procs {
			if p.info.State == api.ProcessRunning {
				procs++
			}
		}
		rec.mu.Unlock()
	}
	w.Family("sandboxd_sandboxes", "gauge", "Sandboxes this node holds, by state (terminated ones are the few it still lists).")
	for _, state := range []string{api.StatePending, api.StateRunning, api.StateSuspended, api.StateTerminated} {
		w.Sample("sandboxd_sandboxes", float64(states[state]), "state", state)
	}
	w.Gauge("sandboxd_processes_running", "Processes running in this node's sandboxes.", float64(procs))

	type poolSize struct{ ready, booting, size int }
	byImage := map[string]*poolSize{}
	var images []string
	for _, p := range pools {
		p.mu.Lock()
		ps := byImage[p.template.Image]
		if ps == nil {
			ps = &poolSize{}
			byImage[p.template.Image] = ps
			images = append(images, p.template.Image)
		}
		ps.ready += len(p.ready)
		ps.booting += p.filling
		ps.size += p.size
		p.mu.Unlock()
	}
	w.Family("sandboxd_pool_ready", "gauge", "Sandboxes booted ahead and ready, by pool image.")
	for _, img := range images {
		w.Sample("sandboxd_pool_ready", float64(byImage[img].ready), "image", img)
	}
	w.Family("sandboxd_pool_booting", "gauge", "Pool sandboxes being booted, by pool image.")
	for _, img := range images {
		w.Sample("sandboxd_pool_booting", float64(byImage[img].booting), "image", img)
	}
	w.Family("sandboxd_pool_target", "gauge", "The size each pool is kept at, by pool image.")
	for _, img := range images {
		w.Sample("sandboxd_pool_target", float64(byImage[img].size), "image", img)
	}

	cordoned := 0.0
	if st.Cordoned {
		cordoned = 1
	}
	w.Gauge("sandboxd_cordoned", "1 when the node takes no new sandboxes.", cordoned)
	w.Gauge("sandboxd_capacity_cpus", "CPUs offered to sandboxes.", st.Capacity.CPUs)
	w.Gauge("sandboxd_free_cpus", "CPUs not given to a sandbox.", st.Free.CPUs)
	w.Gauge("sandboxd_capacity_memory_mb", "Memory offered to sandboxes, MiB.", float64(st.Capacity.MemoryMB))
	w.Gauge("sandboxd_free_memory_mb", "Memory not given to a sandbox, MiB.", float64(st.Free.MemoryMB))
	w.Gauge("sandboxd_capacity_disk_mb", "Disk offered to sandboxes, MiB.", float64(st.Capacity.DiskMB))
	w.Gauge("sandboxd_free_disk_mb", "Disk not given to a sandbox, MiB.", float64(st.Free.DiskMB))

	m := s.metrics()
	m.creates.Write(w, "sandboxd_creates_total", "Create requests, by response status.")
	m.createLat.Write(w, "sandboxd_create_seconds", "Time to create a sandbox, successful creates only.")
}
