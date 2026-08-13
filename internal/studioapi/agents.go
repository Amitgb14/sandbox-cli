package studioapi

import (
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// bakedAgents are the adapters carried in the base image. The rest are installed
// into the persisted HOME on first use. This mirrors assets/Dockerfile, which is
// the only place the answer lives — a descriptor says what runs inside the
// container, not how the binary got there.
var bakedAgents = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "opencode": true,
}

// agentAuth reports where this agent's login is persisted and whether it is
// there yet. It stats the directory and reads its modification time; it never
// opens anything inside, because what is inside is an OAuth refresh token.
func agentAuth(persistDir string) AgentAuth {
	path := config.AgentStateDir(persistDir)
	if path == "" {
		return AgentAuth{}
	}
	a := AgentAuth{Path: path}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		a.Persisted = true
		a.LastSeen = fi.ModTime().UTC().Format(time.RFC3339)
	}
	return a
}

// handleAgents lists the agents this API can launch. Only names with a verified
// headless mode are ever registered in internal/agents (see its package doc),
// which is exactly the constraint POST /runs needs: every run this API starts is
// detached, and an agent that stops to ask permission would hang forever with no
// terminal attached to answer it.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	names := agents.Names()
	// Read once for the whole listing rather than per agent: it is one file, and
	// re-reading it per row would let two rows of one response disagree.
	reg := agentctx.LoadRegistry()
	out := make([]AgentInfo, 0, len(names))
	for _, n := range names {
		d, ok := agents.Lookup(n)
		if !ok {
			continue
		}
		envAllow := append([]string(nil), d.EnvAllow...)
		sort.Strings(envAllow)
		env := append([]string(nil), d.Env...)
		if env == nil {
			env = []string{}
		}
		// The prompt is elided rather than filled with a sample: this is shown as
		// "what would run", and a placeholder in argv position reads as a value
		// someone chose.
		invocation := d.Invocation("<prompt>", nil)

		delivery := "npm"
		if bakedAgents[d.Name] {
			delivery = "baked"
		}

		// An agent with no verified descriptor is reported untracked rather than
		// guessed at, which is the same bargain agentctx makes everywhere else: a
		// store that was never confirmed is not the same as one that is empty.
		store := agentctx.StateUntracked
		sessions := 0
		if f, ok := reg.Get(d.Name); ok {
			store = f.State
			sessions = f.Sessions
		}

		out = append(out, AgentInfo{
			Name:                 d.Name,
			Label:                d.Name,
			PersistDir:           d.PersistDir,
			EnvAllow:             envAllow,
			Env:                  env,
			HeadlessVerified:     true,
			CanSkipPermissions:   d.CanSkipPermissions(),
			SkipPermissionArgs:   d.SkipPermissionArgs,
			AutonomousInvocation: invocation,
			Delivery:             delivery,
			Auth:                 agentAuth(d.PersistDir),
			// Only claude has either, and the limit is deliberate: no other agent
			// has a status-line hook, and only claude mounts the host's per-project
			// history bucket.
			StatusLine:   d.Name == "claude",
			HistorySync:  d.Name == "claude",
			Sessions:     sessions,
			ContextStore: store,
		})
	}
	writeJSON(w, http.StatusOK, AgentsResponse{Agents: out})
}
