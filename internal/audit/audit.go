// Package audit records what each sandbox run actually did.
//
// It exists to answer one question after the fact: *what did this run execute,
// against what policy, and how did it end?* Before this the sink was a no-op, so
// a run left no trace once its container was gone — the same gap the firewall's
// LOG rules close on the network side.
//
// What it deliberately does not record: any environment *value*. Names, yes —
// which credentials a run was handed is exactly what you want to look up later —
// but never what they were worth. The credential broker exists to keep secret
// values off the argv and out of config files; writing them to a log would hand
// that back.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SessionMeta describes a single sandbox session.
type SessionMeta struct {
	Image   string
	Workdir string
	Command []string

	// Workspace is the host directory mounted at Workdir — the thing this run
	// could actually change.
	Workspace string
	// Agent is the wrapper the run came from ("claude", "codex"); empty for a
	// plain `run`.
	Agent string
	// Branch is the git branch the workspace was on, when it had one.
	Branch string

	// Network is the resolved posture: "default", "none" or "allowlist".
	Network string
	// EgressAllow is the resolved allowlist, when one was in force. Recording it
	// is the point: the domains come from several merged layers, so "what was
	// this run permitted to reach" is otherwise unanswerable afterwards.
	EgressAllow []string
	// EnvNames are the host variables forwarded into the container, by name only.
	EnvNames []string

	// Outcome, filled in once the run has finished.
	ExitCode int
	Duration time.Duration
	Detached bool
}

// Sink records session metadata.
type Sink interface {
	RecordSession(meta SessionMeta)
}

// NopSink discards everything. Used by tests, and by any caller that has not
// wired a real sink.
type NopSink struct{}

// RecordSession does nothing.
func (NopSink) RecordSession(SessionMeta) {}

// record is the on-disk shape, kept separate from SessionMeta so the field names
// in the file stay stable and explicit rather than tracking whatever the Go
// struct happens to be called.
type record struct {
	Time        string   `json:"time"`
	Image       string   `json:"image"`
	Workspace   string   `json:"workspace,omitempty"`
	Workdir     string   `json:"workdir,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	Command     []string `json:"command,omitempty"`
	Network     string   `json:"network,omitempty"`
	EgressAllow []string `json:"egress_allow,omitempty"`
	EnvNames    []string `json:"env_names,omitempty"`
	ExitCode    int      `json:"exit_code"`
	DurationMS  int64    `json:"duration_ms"`
	Detached    bool     `json:"detached,omitempty"`
}

// JSONLSink appends one line per run to a file.
//
// Append-only and best-effort: an unwritable log must never fail a run, because
// the run is what the user asked for and the record is a courtesy. The failure
// is silent for the same reason — warning on every invocation about a log nobody
// asked for would be worse than the missing line.
type JSONLSink struct {
	Path string
}

// NewJSONLSink returns a sink writing to dir/sessions.jsonl, or a NopSink when
// dir is empty (no home directory — not an error worth failing a run over).
func NewJSONLSink(dir string) Sink {
	if dir == "" {
		return NopSink{}
	}
	return &JSONLSink{Path: filepath.Join(dir, "sessions.jsonl")}
}

// RecordSession appends meta as one JSON object.
func (s *JSONLSink) RecordSession(meta SessionMeta) {
	if s == nil || s.Path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return
	}
	line, err := json.Marshal(record{
		Time:        time.Now().Format(time.RFC3339),
		Image:       meta.Image,
		Workspace:   meta.Workspace,
		Workdir:     meta.Workdir,
		Agent:       meta.Agent,
		Branch:      meta.Branch,
		Command:     meta.Command,
		Network:     meta.Network,
		EgressAllow: meta.EgressAllow,
		EnvNames:    meta.EnvNames,
		ExitCode:    meta.ExitCode,
		DurationMS:  meta.Duration.Milliseconds(),
		Detached:    meta.Detached,
	})
	if err != nil {
		return
	}
	// 0600: this names the projects someone worked on and the credentials each
	// run was handed. Same treatment as the rescue manifests and the contexts
	// registry.
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}
