package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Records on disk, for a sandboxd that leaves its sandboxes running when it
// exits (sandboxd --keep-sandboxes) so it can be upgraded or restarted without
// interrupting them. The backend keeps the VMs; this keeps what the server
// knows about each one, which the VM cannot tell it: its name, labels,
// policy, schedule, environment and the next process number.
//
// A record holds the sandbox's environment values, secrets included, because
// a sandbox taken back without them would start its next process without the
// environment it was created with. So the directory is the operator's alone —
// 0700, each file 0600 — like the token file, and a record is deleted when its
// sandbox ends. Nothing else of the server's is written: process output and
// metrics are lost with the processes, which a restart ends anyway.

// keptRecord is one sandbox as written to RecordDir.
type keptRecord struct {
	Sandbox api.Sandbox       `json:"sandbox"`
	Env     map[string]string `json:"env,omitempty"`
	// NextPID is the last process number handed out, so a taken-back sandbox
	// never gives a new process the number of one a client still remembers.
	NextPID int `json:"next_pid"`
}

func (s *Server) recordPath(id string) string { return filepath.Join(s.RecordDir, id+".json") }

// persist writes rec's record, replacing the last one in a single rename so a
// crash leaves the old record or the new, never half of one. Records are
// written one at a time, each from a copy taken under that lock, so an older
// copy can never land after a newer one.
func (s *Server) persist(rec *record) {
	if s.RecordDir == "" {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	rec.mu.Lock()
	kr := keptRecord{Sandbox: rec.sbx, Env: rec.env, NextPID: rec.nextPID}
	rec.mu.Unlock()
	if kr.Sandbox.State == api.StateTerminated {
		s.removeRecord(kr.Sandbox.ID)
		return
	}
	data, err := json.Marshal(kr)
	if err == nil {
		err = writeFileAtomic(s.recordPath(kr.Sandbox.ID), data)
	}
	if err != nil {
		// Logged, not returned: the sandbox is running and serving. What it
		// costs is this sandbox across the next restart, and the operator
		// learns that here rather than then.
		s.logf("sandbox %s: writing its record: %v", kr.Sandbox.ID, err)
	}
}

// forget deletes a terminated sandbox's record.
func (s *Server) forget(id string) {
	if s.RecordDir == "" {
		return
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	s.removeRecord(id)
}

func (s *Server) removeRecord(id string) {
	if err := os.Remove(s.recordPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logf("sandbox %s: removing its record: %v", id, err)
	}
}

func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".record-*") // 0600
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// Restore takes back the sandboxes an earlier sandboxd left running: each
// one the backend kept and the server has a record for is served again, as
// it was, with no processes. Called once, before the handler serves.
//
// The two lists are matched, never trusted alone. A VM the backend kept with
// no record is terminated: without a record nothing says whose it is or what
// it may reach. A record whose VM did not survive is deleted. It returns how
// many sandboxes were taken back.
func (s *Server) Restore(ctx context.Context) (int, error) {
	if s.RecordDir == "" {
		return 0, nil
	}
	if err := os.MkdirAll(s.RecordDir, 0o700); err != nil {
		return 0, err
	}
	// The records hold secrets. Refused rather than repaired: a directory
	// others could read may already have been read.
	if fi, err := os.Stat(s.RecordDir); err != nil {
		return 0, err
	} else if fi.Mode().Perm()&0o077 != 0 {
		return 0, fmt.Errorf("records directory %s is readable by other users (mode %v); it holds sandboxes' environment values", s.RecordDir, fi.Mode().Perm())
	}
	if s.now == nil {
		s.now = time.Now
	}
	var kept map[string]backend.Kept
	if k, ok := s.Backend.(backend.Keeper); ok {
		kept = k.Kept()
	}
	des, err := os.ReadDir(s.RecordDir)
	if err != nil {
		return 0, err
	}
	restored := map[string]*record{}
	for _, de := range des {
		name := de.Name()
		path := filepath.Join(s.RecordDir, name)
		id, isRecord := strings.CutSuffix(name, ".json")
		if !isRecord || !spec.ValidID(id) || !de.Type().IsRegular() {
			// A temporary file a crash left behind, or nothing of ours.
			if strings.HasPrefix(name, ".record-") {
				_ = os.Remove(path)
			}
			continue
		}
		rec, err := readRecord(path, id)
		if err != nil {
			s.logf("sandbox %s: its record is unreadable, so it is not taken back: %v", id, err)
			_ = os.Remove(path)
			continue
		}
		k, ok := kept[id]
		if !ok {
			_ = os.Remove(path) // its VM did not survive the restart
			continue
		}
		// The policy may have been tightened while sandboxd was down. A
		// sandbox the current policy would refuse is not served under the
		// old one: it is ended, as untrusted state may only tighten.
		if _, err := spec.ResolveNetworkUpdate(&rec.sbx.Network, s.Policy); err != nil {
			s.logf("sandbox %s: not taken back: its network policy is no longer allowed: %v", id, err)
			_ = os.Remove(path)
			continue // not restored, so terminated below
		}
		now := s.now()
		rec.sbx.State = api.StateRunning
		if k.Suspended {
			rec.sbx.State = api.StateSuspended
		}
		rec.sbx.Snapshotting = nil // a snapshot in progress ended with the old process
		rec.procs = map[int]*procRecord{}
		// The idle clock starts again: the time sandboxd was down is not
		// time anyone left the sandbox alone.
		rec.lastActive, rec.lastScheduled = now, now
		restored[id] = rec
	}
	for id := range kept {
		if _, ok := restored[id]; ok {
			continue
		}
		if err := s.Backend.Terminate(ctx, id); err != nil {
			s.logf("sandbox %s: terminating a sandbox left running without a record: %v", id, err)
			continue
		}
		s.logf("sandbox %s: terminated: left running without a record", id)
	}
	s.mu.Lock()
	if s.sandboxes == nil {
		s.sandboxes = map[string]*record{}
	}
	for id, rec := range restored {
		s.sandboxes[id] = rec
	}
	s.mu.Unlock()
	for id, rec := range restored {
		s.persist(rec) // its state may have changed with the restart
		s.logf("sandbox %s: taken back from an earlier run", id)
	}
	return len(restored), nil
}

func readRecord(path, id string) (*record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var kr keptRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&kr); err != nil {
		return nil, err
	}
	if kr.Sandbox.ID != id {
		return nil, fmt.Errorf("it names sandbox %q", kr.Sandbox.ID)
	}
	if kr.NextPID < 0 {
		return nil, errors.New("a negative process number")
	}
	return &record{sbx: kr.Sandbox, env: kr.Env, nextPID: kr.NextPID}, nil
}
