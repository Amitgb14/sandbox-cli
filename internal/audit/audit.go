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
//
// The guest command *is* recorded verbatim, and that is the known soft edge in
// the rule above: an argv is the other classic place a token ends up
// (`-- curl -H "Authorization: Bearer …"`). It is kept because a run log that
// cannot say what ran answers nothing, and because redaction here would have to
// guess — a heuristic that scans argv for secrets misses the ones it does not
// recognise while implying it caught them all, which is worse than a documented
// limitation. Treat sessions.jsonl as sensitive; it is written 0600 for this
// reason as much as for the project names.
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

	// Engine is the container engine that executed the run ("docker", "podman").
	// Recorded for the reason the detached-run labels are: a fact not stamped is
	// one no later command can recover, and "which engine ran this" is not
	// derivable from anything else in the line.
	Engine string
	// Runtime is the OCI runtime the run asked the engine for, empty meaning the
	// host default (runc everywhere this tool has met). The same kind of fact as
	// Network: it is resolved from several layers, so "what boundary did this run
	// actually get" is otherwise unanswerable afterwards — and it is the one
	// setting that changes the *kind* of boundary rather than its degree.
	//
	// Asked for, and that is not a hedge here: the engine refuses to start a
	// container on a runtime it has not registered, so a run that produced this
	// line got what it named. The listing reads the runtime back from the engine
	// instead (runtime.ContainerInfo.Runtime), because a container that still
	// exists can be asked.
	Runtime string
	// Network is the resolved posture: "default", "none" or "allowlist".
	Network string
	// NetworkName is the network object the container actually joined. Under
	// podman that is per-run rather than shared, so the posture alone no longer
	// identifies it.
	NetworkName string
	// EgressEnforcementRequested is "name" when the run asked for the
	// in-container proxy to decide each connection by hostname, "address" when
	// only the address-matching firewall was asked for, and "" when there was no
	// allowlist. Recorded because `network: allowlist` alone does not say which
	// regime a past run was under, and the two differ in exactly the way that
	// matters: an address-matched run permitted every host sharing an allowlisted
	// address.
	//
	// Named for a *request*, not an outcome, because that is all the host can
	// honestly know. The container takes the proxy path only if
	// `sandbox-egress-proxy` is on its PATH and the `sandbox-proxy` user resolves;
	// otherwise it falls through to address matching. With a user-supplied
	// --image or `image:` those can be absent, and this field previously claimed
	// "name" for a run that was address-matched. Observing it for real needs the
	// entrypoint to report back what it did — worth doing, and until it exists an
	// accurate name beats a confident wrong value.
	EgressEnforcementRequested string
	// EgressAllow is the resolved allowlist, when one was in force. Recording it
	// is the point: the domains come from several merged layers, so "what was
	// this run permitted to reach" is otherwise unanswerable afterwards.
	EgressAllow []string
	// EnvNames are the host variables forwarded into the container, by name only.
	EnvNames []string

	// EgressDeniedReported is how many egress refusals the in-container proxy
	// printed during the run, and EgressDeniedHostsReported a bounded sample of
	// the distinct names involved. Together they answer the question the policy
	// fields cannot: `egress_allow` says what the run was *permitted* to reach,
	// not whether it went looking for anything else.
	//
	// **Reported, not attested**, and the names say so for the same reason
	// EgressEnforcementRequested is named for a request. These lines arrive on the
	// container's stderr, which the agent also writes to — so a compromised agent
	// can print lines that look like denials, and can bury real ones in noise.
	// What the host honestly knows is "this many lines claiming a denial came back
	// from the container", and that is what the field is called.
	//
	// It is still worth recording: the failure mode it catches is the ordinary one
	// — an allowlist that was too narrow, or an agent reaching somewhere nobody
	// expected — and for that a truthful container is the normal case. Making it
	// authoritative needs the proxy reporting over a channel the guest cannot
	// write to.
	//
	// A **pointer** so that "nobody looked" and "looked, and nothing was refused"
	// are different answers. They are not the same fact, and the zero one is the
	// more useful of the two: a recorded 0 is the only positive evidence a run's
	// allowlist was wide enough for everything it actually wanted.
	//
	// nil means the run was not observed, and today that is most runs. Only a
	// non-interactive `run` under an allowlist is watched. The rest:
	//
	//   - **no allowlist** — no proxy runs, so nothing could be refused.
	//   - **interactive** — with a pty docker returns one merged stream, and
	//     reading it would cost the container its terminal size, so sandbox-cli
	//     declines to look (see runtime.newDenyTap for the measurement).
	//   - **detached, which includes every fleet task** — Session.Start never
	//     wires a collector. Note the reason is *not* that the output is gone:
	//     `docker logs` has it, which is the whole supervision story --detach is
	//     built around. Nothing reads it back yet. That is the wrong way round —
	//     an unattended fleet is where an after-the-fact record is worth most —
	//     and it is recorded as follow-up in docs/roadmap/task-4-run-provenance.md.
	//
	// `network` separates the first case from the others after the fact: `default`
	// means there was nothing to refuse, `allowlist` with this field absent means
	// an allowlist was in force and nobody watched.
	//
	// **The host sample is not a random sample, and an adversary picks it.** The
	// count is exact, but the names are the *first* maxDenyHosts distinct ones
	// seen, and a guest can forge denial lines — so one that emits 32 invented
	// names first fills the sample and every real refusal after that is counted
	// but unnamed. The field is called "reported" for exactly this reason; treat
	// the list as a hint and the count as the number.
	EgressDeniedReported      *int
	EgressDeniedHostsReported []string

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
	Engine      string   `json:"engine,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Network     string   `json:"network,omitempty"`
	NetworkName string   `json:"network_name,omitempty"`
	EnforcedBy  string   `json:"egress_enforcement_requested,omitempty"`
	EgressAllow []string `json:"egress_allow,omitempty"`
	EnvNames    []string `json:"env_names,omitempty"`
	// Named for what the host knows, not for what happened — see SessionMeta. A
	// pointer so `omitempty` drops only an unobserved run: a pointer to 0 is
	// non-nil, so "looked, nothing refused" is written as an explicit 0 instead of
	// vanishing the way a bare int would.
	EgressDenied      *int     `json:"egress_denied_reported,omitempty"`
	EgressDeniedHosts []string `json:"egress_denied_hosts_reported,omitempty"`
	ExitCode          int      `json:"exit_code"`
	DurationMS        int64    `json:"duration_ms"`
	Detached          bool     `json:"detached,omitempty"`
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

// maxLogBytes is the size at which the log is rotated. One run is a few hundred
// bytes, so this holds a long history while bounding a file nothing else ever
// prunes — an append-only log with no ceiling is a slow leak in the user's home
// directory.
const maxLogBytes = 8 << 20

// RecordSession appends meta as one JSON object.
func (s *JSONLSink) RecordSession(meta SessionMeta) {
	if s == nil || s.Path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return
	}
	s.rotateIfLarge()
	line, err := json.Marshal(record{
		Time:        time.Now().Format(time.RFC3339),
		Image:       meta.Image,
		Workspace:   meta.Workspace,
		Workdir:     meta.Workdir,
		Agent:       meta.Agent,
		Branch:      meta.Branch,
		Command:     meta.Command,
		Engine:      meta.Engine,
		Runtime:     meta.Runtime,
		Network:     meta.Network,
		NetworkName: meta.NetworkName,
		EnforcedBy:  meta.EgressEnforcementRequested,
		EgressAllow: meta.EgressAllow,
		EnvNames:    meta.EnvNames,

		EgressDenied:      meta.EgressDeniedReported,
		EgressDeniedHosts: meta.EgressDeniedHostsReported,

		ExitCode:   meta.ExitCode,
		DurationMS: meta.Duration.Milliseconds(),
		Detached:   meta.Detached,
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

// MaxGenerations is how many rotated logs are kept beside the current one.
//
// It used to be one, and that quietly deleted history. At the density this
// writes — a few hundred bytes per run — 8 MiB is roughly twelve thousand runs,
// so a single previous generation meant the twelve-thousandth-oldest run
// vanished with nothing recording that it had ever existed. A fleet running
// fifty tasks a day reached that in about eighteen months; one running five
// hundred, in six weeks.
//
// Five generations is ~40 MiB and ~60,000 runs. That is a bounded cost in a
// directory nothing else prunes, and the ceiling is the point: an append-only
// log with no ceiling is a slow leak in someone's home directory, and a log
// that drops the oldest without saying so is worse than one that is capped.
const MaxGenerations = 5

// rotateIfLarge moves the log aside once it passes maxLogBytes, shifting the
// previous generations along and dropping the oldest.
//
// Oldest first, so no rename can overwrite a generation that has not been moved
// yet: .4 becomes .5 before .3 becomes .4. Doing it the other way round loses
// every generation but one, which is the bug this is fixing.
//
// Best-effort like everything else here: if a rename fails the run still
// proceeds and the log simply keeps growing, which is the lesser of the two
// failures.
func (s *JSONLSink) rotateIfLarge() {
	fi, err := os.Stat(s.Path)
	if err != nil || fi.Size() < maxLogBytes {
		return
	}
	// The last generation is removed rather than shifted: something has to be
	// the end, and this is the one place it is decided.
	_ = os.Remove(generationPath(s.Path, MaxGenerations))
	for i := MaxGenerations - 1; i >= 1; i-- {
		_ = os.Rename(generationPath(s.Path, i), generationPath(s.Path, i+1))
	}
	_ = os.Rename(s.Path, generationPath(s.Path, 1))
}

// generationPath names one rotated log. Generation 0 is the live file.
func generationPath(base string, n int) string {
	if n <= 0 {
		return base
	}
	return base + "." + strconv.Itoa(n)
}

// Generations lists the log files that exist, newest first, starting with the
// live one.
//
// Exported because a reader has to know that the history is several files: a
// caller that opens only sessions.jsonl sees the most recent generation and
// reports the rest as though it never happened, which is exactly the failure
// this pairs with.
func Generations(path string) []string {
	var out []string
	for n := 0; n <= MaxGenerations; n++ {
		p := generationPath(path, n)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
