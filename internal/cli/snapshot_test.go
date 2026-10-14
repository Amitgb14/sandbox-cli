package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// snapshotStandIn is an endpoint's /v1/snapshots, enough for the CLI.
type snapshotStandIn struct {
	mu    sync.Mutex
	snaps map[string]api.Snapshot
}

func (s *snapshotStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/snapshots":
		list := api.SnapshotList{Snapshots: []api.Snapshot{}}
		for _, v := range s.snaps {
			list.Snapshots = append(list.Snapshots, v)
		}
		reply(200, list)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/snapshots/"):
		id := strings.TrimPrefix(r.URL.Path, "/v1/snapshots/")
		if _, ok := s.snaps[id]; !ok {
			reply(404, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeNotFound, Message: "no such snapshot"}})
			return
		}
		delete(s.snaps, id)
		w.WriteHeader(204)
	default:
		reply(404, api.ErrorBody{Error: api.ErrorDetail{Code: api.CodeNotFound, Message: "no such endpoint"}})
	}
}

func TestSnapshotLsAndRm(t *testing.T) {
	now := time.Now()
	ep := &snapshotStandIn{snaps: map[string]api.Snapshot{
		"snp_00000000000000a1": {ID: "snp_00000000000000a1", Sandbox: "sbx_0123456789abcdef", Image: "\x1b[31mevil", Kind: "memory", Bytes: 3 << 29, CreatedAt: now.Add(-time.Hour)},
		"snp_00000000000000a2": {ID: "snp_00000000000000a2", Sandbox: "sbx_0123456789abcdef", Image: "python:3.13", Kind: "memory", Bytes: 300 << 20, Scheduled: true, CreatedAt: now},
	}}
	ts := httptest.NewServer(ep)
	defer ts.Close()
	useEndpoint(t, ts.URL)

	out, err := runCLI(t, "snapshot-ls")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	a1, a2 := strings.Index(out, "snp_00000000000000a1"), strings.Index(out, "snp_00000000000000a2")
	if a1 < 0 || a2 < 0 || a2 > a1 {
		t.Errorf("ls is not newest first:\n%s", out)
	}
	for _, want := range []string{"1.5 GiB", "300 MiB", "schedule", "request"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("ls printed an escape sequence from the server: %q", out)
	}

	// One that does not exist is reported and fails the command, but the
	// others named are still deleted.
	out, err = runCLI(t, "snapshot-rm", "snp_00000000000000ff", "snp_00000000000000a1")
	if err == nil {
		t.Error("deleting a snapshot that does not exist succeeded")
	}
	if !strings.Contains(out, "deleted snp_00000000000000a1") {
		t.Errorf("rm: %v\n%s", err, out)
	}
	if _, ok := ep.snaps["snp_00000000000000a1"]; ok || len(ep.snaps) != 1 {
		t.Errorf("left: %v", ep.snaps)
	}
	if _, err := runCLI(t, "snapshot-rm"); err == nil {
		t.Error("snapshot-rm with no id was accepted")
	}
}
