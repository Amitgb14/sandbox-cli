package studioapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// Whether a provider has been answering, over time.
//
// Everything else on the Routing screen is derived from records that already
// exist — the run log knows which agent ran and why. Uptime is the exception and
// it is worth being explicit about the cost: **nothing on this machine records
// whether a provider was up at a moment nobody asked**. The on-demand probe
// behind `GET /v1/routing` is cached for thirty seconds and then discarded, so a
// history of it has to be collected, which means the daemon making outbound
// requests on a timer whether or not anybody launches anything.
//
// Three decisions follow from not wanting that to be a surprise:
//
//   - **Off unless asked for.** `-probe-interval 0` disables it and the daemon
//     says which it is doing at startup. The default is five minutes, which is
//     twelve HEAD requests per provider per hour.
//   - **A gap is not an outage.** A daemon that was not running recorded
//     nothing, and a bucket with no samples reports *unknown* rather than down.
//     Reading silence as failure would show every laptop's overnight as an
//     incident.
//   - **It records providers, not you.** A sample is a hostname, a timestamp and
//     whether something answered. No run, no prompt, no repository — the same
//     rule audit.SessionMeta keeps about environment values.
//
// The file is a bounded ring per agent, so it cannot grow without limit on a
// daemon left running for a month.

// probeRetention is how much history is kept per agent: a week at the default
// interval. Long enough to cover "was it down yesterday too", short enough that
// the file stays small enough to rewrite in one go.
const probeRetention = 7 * 24 * time.Hour

// maxProbeSamples bounds the ring regardless of interval, so a one-minute
// interval cannot turn the file into something that has to be streamed.
const maxProbeSamples = 4096

// probeSample is one answer from one provider.
type probeSample struct {
	At time.Time `json:"at"`
	// Up is whether anything answered. A 401 is up: the probe carries no
	// credentials, and the endpoint answering at all is the question.
	Up bool `json:"up"`
	// Reason is why not, when not. Kept because "timed out" and "provider
	// answered 503" are different incidents, and because it is what distinguishes
	// an outage from a laptop with no network.
	Reason string `json:"reason,omitempty"`
}

// probeLog is the persisted history, keyed by agent.
type probeLog struct {
	mu      sync.Mutex
	path    string
	Samples map[string][]probeSample `json:"samples"`
}

func loadProbeLog(path string) *probeLog {
	l := &probeLog{path: path, Samples: map[string][]probeSample{}}
	b, err := os.ReadFile(path)
	if err != nil {
		// No file is the ordinary state of a daemon that has not probed yet, and
		// an unreadable one is not worth refusing to start over: this is a chart,
		// not a boundary control.
		return l
	}
	var on struct {
		Samples map[string][]probeSample `json:"samples"`
	}
	if err := json.Unmarshal(b, &on); err != nil || on.Samples == nil {
		return l
	}
	l.Samples = on.Samples
	return l
}

// record appends one sample and rewrites the file.
//
// Rewritten whole rather than appended to, because the ring drops old samples on
// every write and an append-only file would need compaction — a second mechanism
// for a file that is a few tens of kilobytes.
func (l *probeLog) record(agent string, s probeSample) {
	// Nil-tolerant, like every rescue.Snapshotter method and for the same reason:
	// a Server built as a struct literal (every test in this package) should not
	// need to remember this, and a chart is never worth a panic in a handler.
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := s.At.Add(-probeRetention)
	kept := make([]probeSample, 0, len(l.Samples[agent])+1)
	for _, old := range l.Samples[agent] {
		if old.At.After(cutoff) {
			kept = append(kept, old)
		}
	}
	kept = append(kept, s)
	if len(kept) > maxProbeSamples {
		kept = kept[len(kept)-maxProbeSamples:]
	}
	l.Samples[agent] = kept
	l.saveLocked()
}

func (l *probeLog) saveLocked() {
	if l.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	b, err := json.Marshal(struct {
		Samples map[string][]probeSample `json:"samples"`
	}{l.Samples})
	if err != nil {
		return
	}
	// Written via a temp file so a daemon killed mid-write leaves the previous
	// history rather than a truncated file the next start cannot parse.
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, l.path)
}

// buckets summarises one agent's history into n slots over the given window,
// oldest first.
func (l *probeLog) buckets(agent string, window time.Duration, n int, now time.Time) []ProbeBucket {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	samples := append([]probeSample(nil), l.Samples[agent]...)
	l.mu.Unlock()
	sort.Slice(samples, func(i, j int) bool { return samples[i].At.Before(samples[j].At) })

	out := make([]ProbeBucket, n)
	slot := window / time.Duration(n)
	start := now.Add(-window)
	for i := range out {
		out[i].At = start.Add(time.Duration(i) * slot)
	}
	for _, s := range samples {
		i := int(s.At.Sub(start) / slot)
		if i < 0 || i >= n {
			continue
		}
		if s.Up {
			out[i].Up++
		} else {
			out[i].Down++
			if out[i].Reason == "" {
				out[i].Reason = s.Reason
			}
		}
	}
	return out
}

// startProbing begins sampling every routable provider on an interval.
//
// Called by the daemon's main rather than lazily on first request: the point is
// to know about an outage that happened while nobody was looking, which requires
// sampling when nobody is looking. An interval of zero means do not sample at
// all, and the endpoint then answers with whatever history is already on disk.
func (s *Server) StartProbing(ctx context.Context, interval time.Duration) {
	s.ProbeInterval = interval
	if interval <= 0 {
		return
	}
	go func() {
		// One immediately, so a daemon that has just started is not a blank strip
		// for the first five minutes.
		s.sampleProviders(ctx)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sampleProviders(ctx)
			}
		}
	}()
}

// sampleProviders probes once and records the answers.
func (s *Server) sampleProviders(ctx context.Context) {
	// force=true: the cached answer is for the screen, and recording the same
	// thirty-second-old reading twice would draw a flat strip out of one probe.
	for _, p := range probeAll(ctx, runningProviders(s), gatewayHosts(s), true) {
		if !p.Probed {
			// Nothing was asked, so there is nothing to record. An unprobeable
			// agent must not accumulate a history of implied uptime.
			continue
		}
		s.Probes.record(p.Agent, probeSample{At: time.Now(), Up: p.Reachable, Reason: p.Reason})
	}
}

// handleProbeHistory is GET /v1/routing/history.
func (s *Server) handleProbeHistory(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 168 {
			hours = n
		}
	}
	const slots = 48
	window := time.Duration(hours) * time.Hour
	now := time.Now()

	out := make([]ProviderHistory, 0, 4)
	for _, p := range probeAll(r.Context(), runningProviders(s), gatewayHosts(s), false) {
		if !p.Routable {
			continue
		}
		buckets := s.Probes.buckets(p.Agent, window, slots, now)
		var up, down int
		for _, b := range buckets {
			up += b.Up
			down += b.Down
		}
		if up+down == 0 {
			// No history at all for this agent. Reported with its empty buckets
			// rather than omitted, so the strip shows a row of unknowns and says
			// why, instead of an agent silently missing from the list.
			out = append(out, ProviderHistory{Agent: p.Agent, Buckets: buckets})
			continue
		}
		out = append(out, ProviderHistory{
			Agent:   p.Agent,
			Buckets: buckets,
			Uptime:  float64(up) / float64(up+down),
			Samples: up + down,
		})
	}
	writeJSON(w, http.StatusOK, ProbeHistoryResponse{
		Hours:     hours,
		Interval:  int(s.ProbeInterval / time.Second),
		Providers: out,
	})
}

// probesPath is where the history lives: beside Studio's other state, not in any
// repository.
func probesPath() string {
	root := config.ConfigRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "studio", "probes.json")
}
