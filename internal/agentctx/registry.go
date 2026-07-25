package agentctx

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// registryVersion guards the on-disk shape. The registry is a cache of facts
// that can always be re-established by probing, so a version it does not
// recognise is discarded rather than migrated: one extra probe is cheaper than
// any migration code, and far cheaper than a subtly misread record.
const registryVersion = 1

// Registry is the persisted answer to "where does each agent keep its sessions
// on this machine?". It lives outside every repository, next to the context
// manifests, and outlives both the container and the checkout — which is the
// point. Probing is cheap but not free, and more importantly it is only possible
// while the agent's home is present; recording the result means a later command
// can still resolve a store on a day the probe would come up empty.
type Registry struct {
	Version  int                `json:"version"`
	ProbedAt time.Time          `json:"probed_at"`
	Agents   map[string]Finding `json:"agents"`

	path string // not serialized
}

// RegistryPath is the registry file, e.g. ~/.config/sandbox/contexts/stores.json.
// Empty when there is no home directory to put it in.
func RegistryPath() string {
	dir := config.ContextsDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "stores.json")
}

// LoadRegistry reads the registry, returning an empty one when it does not exist
// yet. A registry that cannot be parsed is also returned empty rather than as an
// error: it is a cache, and refusing to work because a cache went bad would be
// the wrong trade.
func LoadRegistry() *Registry {
	return loadRegistryAt(RegistryPath())
}

func loadRegistryAt(path string) *Registry {
	r := &Registry{Version: registryVersion, Agents: map[string]Finding{}, path: path}
	if path == "" {
		return r
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return r
	}
	var on Registry
	if err := json.Unmarshal(data, &on); err != nil || on.Version != registryVersion {
		return r
	}
	if on.Agents != nil {
		r.Agents = on.Agents
	}
	r.ProbedAt = on.ProbedAt
	return r
}

// Get returns the recorded finding for an agent.
func (r *Registry) Get(agent string) (Finding, bool) {
	f, ok := r.Agents[agent]
	return f, ok
}

// Record merges a fresh probe into the registry.
func (r *Registry) Record(f Finding) {
	if r.Agents == nil {
		r.Agents = map[string]Finding{}
	}
	old, had := r.Agents[f.Agent]
	if !had {
		old = Finding{}
	}
	r.Agents[f.Agent] = Merge(old, f)
	if f.CheckedAt.After(r.ProbedAt) {
		r.ProbedAt = f.CheckedAt
	}
}

// Findings returns the recorded findings, agent name order, for display.
func (r *Registry) Findings() []Finding {
	out := make([]Finding, 0, len(r.Agents))
	for _, f := range r.Agents {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Agent < out[j].Agent })
	return out
}

// Merge folds a fresh probe into what was already known. It is pure, and it is
// the rule that makes the registry worth keeping:
//
//   - A probe that finds sessions always wins, and inherits the original
//     FirstSeen so "known since" stays true across re-probes.
//   - A probe that finds nothing never erases a store that was verified before.
//     The agent's home may simply not be mounted today, or the user may have run
//     with --no-persist-auth; forgetting a path we once confirmed would turn a
//     temporary absence into permanent amnesia. The old record is kept and marked
//     Stale, so callers can still use it and still say how old it is.
func Merge(old, fresh Finding) Finding {
	if fresh.State == StateVerified {
		out := fresh
		out.FirstSeen = old.FirstSeen
		if out.FirstSeen.IsZero() {
			out.FirstSeen = fresh.CheckedAt
		}
		return out
	}
	if old.State == StateVerified {
		out := old
		out.CheckedAt = fresh.CheckedAt
		out.Stale = true
		// Descriptor-derived fields still come from the fresh probe: the store may
		// be out of sight, but how to resume it is a property of this build.
		out.Resume, out.MintIDFlag, out.Note, out.Format = fresh.Resume, fresh.MintIDFlag, fresh.Note, fresh.Format
		return out
	}
	out := fresh
	out.FirstSeen = old.FirstSeen
	return out
}

// Save writes the registry atomically, so an interrupted write cannot leave a
// half-written file where the verified paths should be.
func (r *Registry) Save() error {
	if r.path == "" {
		return fmt.Errorf("cannot determine the sandbox config directory (no HOME?)")
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	r.Version = registryVersion
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

// Refresh probes every known store, folds the results into the persisted
// registry and saves it. The returned findings are what was just recorded — the
// merged view, not the raw probe, so a caller shows the same thing the registry
// will hand out later.
func Refresh(roots Roots, now time.Time) (*Registry, error) {
	reg := LoadRegistry()
	for _, f := range ProbeAll(roots, now) {
		reg.Record(f)
	}
	return reg, reg.Save()
}

// Resolve answers "where are this agent's sessions?" from the registry alone,
// probing once if the agent has never been recorded. This is the call the rest
// of sandbox-cli should use: it costs a file read on the common path, and it
// still answers on a machine where the store is momentarily out of reach.
func Resolve(agent string, roots Roots, now time.Time) (Finding, bool) {
	reg := LoadRegistry()
	if f, ok := reg.Get(agent); ok && f.State == StateVerified {
		return f, true
	}
	s, known := Lookup(agent)
	if !known {
		return Untracked(agent, now), false
	}
	f := s.Probe(roots, now)
	reg.Record(f)
	// A failed save is not worth failing the caller over: the probe result is
	// still correct, it just will not be remembered.
	_ = reg.Save()
	merged, _ := reg.Get(agent)
	return merged, merged.State == StateVerified
}
