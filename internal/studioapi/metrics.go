package studioapi

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// handleRunMetrics is GET /runs/{id}/metrics. Without stream=1 it returns one
// point-in-time sample; with it, the same shape is pushed as Server-Sent Events
// every two seconds — the API equivalent of the CLI's live gauge, for a client
// that wants a chart rather than a terminal.
func (s *Server) handleRunMetrics(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if !r.URL.Query().Has("stream") {
		m, err := s.sampleOne(r.Context(), c.ID)
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this connection"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		m, err := s.sampleOne(r.Context(), c.ID)
		if err != nil {
			writeSSE(w, "error", ErrorResponse{Error: err.Error()})
		} else {
			writeSSE(w, "metrics", m)
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) sampleOne(ctx context.Context, id string) (RunMetrics, error) {
	rows, err := s.sampleMetrics(ctx, []string{id})
	if err != nil {
		return RunMetrics{}, err
	}
	if len(rows) == 0 {
		return RunMetrics{}, fmt.Errorf("no stats for %s (is it running?)", shortID(id))
	}
	return rows[0], nil
}

// handleStats is GET /stats: one sample per live sandbox container, host-wide —
// the API equivalent of `sandbox-cli stats --once`.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	infos, err := s.listRuns(r.Context(), false, nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	ids := make([]string, 0, len(infos))
	for _, c := range infos {
		if c.Running() {
			ids = append(ids, c.ID)
		}
	}
	rows, err := s.sampleMetrics(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, StatsResponse{Runs: rows, SampledAt: time.Now()})
}

// sampleMetrics shells out to `docker stats --no-stream`, the same command
// internal/cli's sampleSandboxStats uses, and parses its formatted columns into
// numbers a JSON client can chart directly instead of re-parsing "12.3MiB /
// 1.9GiB" itself.
func (s *Server) sampleMetrics(ctx context.Context, ids []string) ([]RunMetrics, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	sampledAt := time.Now()
	args := append([]string{"stats", "--no-stream",
		"--format", "{{.ID}}|{{.MemUsage}}|{{.MemPerc}}|{{.CPUPerc}}|{{.PIDs}}"}, ids...)
	out, err := exec.CommandContext(ctx, s.Engine, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s stats: %w", s.Engine, err)
	}
	var rows []RunMetrics
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 5)
		if len(f) != 5 {
			continue
		}
		used, limit := parseMemUsage(f[1])
		rows = append(rows, RunMetrics{
			ID:            shortID(f[0]),
			MemUsageBytes: used,
			MemLimitBytes: limit,
			MemPercent:    parsePercent(f[2]),
			CPUPercent:    parsePercent(f[3]),
			PIDs:          parseIntOr(f[4], 0),
			SampledAt:     sampledAt,
		})
	}
	return rows, nil
}

// parseMemUsage parses docker's "12.34MiB / 1.952GiB" MemUsage column. Either
// side may be "--" (never observed, kept for the same reason parsePercent
// tolerates it) and yields 0 rather than an error, since this is a display
// number, not something a caller should have to fail a request over.
func parseMemUsage(s string) (used, limit int64) {
	parts := strings.SplitN(s, "/", 2)
	used = parseSize(strings.TrimSpace(parts[0]))
	if len(parts) == 2 {
		limit = parseSize(strings.TrimSpace(parts[1]))
	}
	return used, limit
}

// parseSize parses a docker-formatted byte size like "1.95GiB" or "512kB" (the
// two unit families docker's go-units emits: binary Ki/Mi/Gi/Ti and decimal
// k/M/G/T, both suffixed B) into bytes.
func parseSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "--" {
		return 0
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3},
		{"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil {
				return 0
			}
			return int64(n * u.factor)
		}
	}
	n, _ := strconv.ParseFloat(s, 64)
	return int64(n)
}

// parsePercent parses "0.50%", returning 0 for docker's "--" (printed when the
// platform cannot report a metric, e.g. memory stats on some non-Linux hosts).
func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	if s == "" || s == "--" {
		return 0
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseIntOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}
