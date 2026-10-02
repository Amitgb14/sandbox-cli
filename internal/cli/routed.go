package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// A routed run is a wrapper run with somewhere to fall through to:
// `--fallback codex`, or `routing: [claude, codex]` in the user's config. The
// two rules are internal/routing's — probe each provider before creating a
// sandbox for it, and retry only a run that failed **having changed nothing**
// — and what is new in the rewrite is only where their inputs come from.
//
//   - "Changed nothing" is bring-back's answer. Bring-back commits whatever the
//     agent left before bundling it, so a bring-back that succeeded with no ref
//     is a workspace that ended as it began. Anything that cannot say — a
//     bring-back that failed, --bind, --no-bring-back, no repository — is
//     unknown, and routing reads unknown as work done.
//   - Every attempt is a **fresh sandbox** cloned from the host repository, not
//     the failed one reused. A guest that lied about having done nothing then
//     cannot hand anything to the next agent: there is nothing of it left.
//   - The briefing is read out of the failed sandbox before it is terminated,
//     and written into the next one over the API (handoff.GuestDir).

// transcriptStore is where an agent keeps this run's conversation inside the
// sandbox, and the reader for its format. Only agents whose format is verified
// (agentctx) are here, so only their conversation crosses; any other agent's
// briefing is the file ledger alone, which handoff treats as an ordinary case
// rather than a failure.
type transcriptStore struct {
	dir    string
	depth  int    // directories below dir that sessions are sharded into
	prefix string // a session file's name starts with this
	parse  func(io.Reader, int) ([]agentctx.Message, error)
}

var transcriptStores = map[string]transcriptStore{
	// The bucket name is Claude Code's spelling of the working directory.
	"claude": {dir: workspace.GuestHome + "/.claude/projects/-workspace", parse: agentctx.ParseTranscript},
	// Sharded by date, YYYY/MM/DD, not by project; a fresh sandbox holds only
	// this run's sessions, so the shard is not a question.
	"codex": {dir: workspace.GuestHome + "/.codex/sessions", depth: 3, prefix: "rollout-", parse: agentctx.ParseCodexTranscript},
}

// maxTranscripts and maxTranscriptDirs bound the search for this run's
// conversation. A fresh sandbox holds one; the directory is the agent's to
// write, and a thousand planted files or directories should not mean a
// thousand reads.
const (
	maxTranscripts    = 8
	maxTranscriptDirs = 16
)

// configuredRouting is the chain's fallbacks and the probe overrides: flags
// first, then the user's config. routing: and providers: are refused from a
// project .sandbox.yaml (policy/trust.go) — choosing the agent chooses which
// login is in reach.
func configuredRouting(rf *runFlags, primary string) (fallbacks []string, providers map[string]string, err error) {
	project := rf.project
	if project == "" {
		project, _ = os.Getwd()
	}
	ov, err := rf.overrides()
	if err != nil {
		return nil, nil, err
	}
	cfg, err := loadConfig(project, rf.configPath, rf.profile, ov)
	if err != nil {
		return nil, nil, err
	}
	fallbacks = rf.fallback
	if len(fallbacks) == 0 {
		for _, name := range cfg.Routing {
			if name != primary {
				fallbacks = append(fallbacks, name)
			}
		}
	}
	return fallbacks, cfg.Providers, nil
}

// routedRun runs a wrapper's agent with its chain. A chain of one is exactly an
// ordinary run: no probe, the user's own argv.
func routedRun(ctx context.Context, rf *runFlags, primary agents.Descriptor, agentArgs []string) (int, error) {
	argv := append(append([]string{}, primary.Command...), agentArgs...)
	fallbacks, providers, err := configuredRouting(rf, primary.Name)
	if err != nil {
		return 1, err
	}
	if len(fallbacks) == 0 {
		return execute(ctx, rf, runSpec{argv: argv, agent: &primary})
	}
	if _, ok := agents.Lookup(primary.Name); !ok {
		return 1, fmt.Errorf("--fallback is not available for %s: it has no verified non-interactive mode, "+
			"so a run cannot be re-targeted at it (routable agents: %s)", primary.Name, strings.Join(agents.Names(), ", "))
	}
	if rf.detach {
		return 1, fmt.Errorf("--fallback needs a run in the foreground: a detached run's exit is not watched, so nothing would fall through")
	}

	// Whether anybody is watching decides what a fallback may become: an
	// unattended one runs headless, an attended one keeps its interactive UI.
	// Each is the user saying so, never inferred.
	unattended := !(isTerminal(os.Stdin) && isTerminal(os.Stdout)) || asksForAutonomy(primary.Name, agentArgs)
	chain, err := routing.Resolve(primary.Name, fallbacks, unattended)
	if err != nil {
		return 1, err
	}
	// Not an error until a fallback needs it: the primary runs what was typed.
	prompt, promptErr := promptFrom(agentArgs)

	var skipped []string
	var carried *handoff.Export
	// One id for the whole episode, on every attempt including the first:
	// without it the two sandboxes of a failover read as two unrelated runs,
	// and "did routing help" is unanswerable.
	routeID := routing.NewID()
	var routedFrom, routeReason string
	for i, name := range chain {
		d, _ := agents.Lookup(name)
		if avail := routing.Probe(ctx, name, providers); !avail.Reachable {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", name, avail.Reason))
			fmt.Fprintf(os.Stderr, "sandbox-cli: skipping %s — %s\n", name, avail.Reason)
			if routedFrom == "" {
				routedFrom = name
			}
			routeReason = strings.Join(skipped, "; ")
			if i == len(chain)-1 {
				return 1, fmt.Errorf("no agent in the chain %s is available: %s", chain, strings.Join(skipped, ", "))
			}
			continue
		}
		if i > 0 {
			fmt.Fprintf(os.Stderr, "sandbox-cli: routing %s → %s", chain.Primary(), name)
			if len(skipped) > 0 {
				fmt.Fprintf(os.Stderr, " (skipped %s)", strings.Join(skipped, ", "))
			}
			fmt.Fprintf(os.Stderr, "\nsandbox-cli: %s runs with its own login; this is a different agent, not a resumed one\n", name)
		}

		rs := runSpec{argv: argv, agent: &d, result: &runResult{}, labels: map[string]string{
			"route.id": routeID, "route.attempt": fmt.Sprint(i + 1),
		}}
		if routedFrom != "" {
			rs.labels["route.from"] = routedFrom
			rs.labels["route.reason"] = truncateLabel(routeReason)
		}
		if i == 0 {
			// The wrapper's descriptor, which may carry interactive-only
			// settings the headless table does not.
			rs.agent = &primary
		} else {
			if promptErr != nil {
				return 1, promptErr
			}
			rs.argv = fallbackArgv(d, prompt, carried, unattended)
			if carried != nil {
				rs.before = writeBriefing(carried)
			}
		}
		var collected []agentctx.Message
		rs.after = func(ctx context.Context, c *api.Client, sandbox string) {
			collected = readTranscript(ctx, c, sandbox, name)
		}
		code, err := execute(ctx, rf, rs)
		if err != nil || code == 0 {
			return code, err
		}

		over, why := routing.ShouldFailOver(routing.Outcome{Agent: name, ExitCode: code, WorkspaceChanged: rs.result.changed})
		if !over || i == len(chain)-1 {
			if over {
				fmt.Fprintf(os.Stderr, "sandbox-cli: %s %s, and it was the last agent in the chain\n", name, why)
			}
			return code, nil
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s %s — trying %s\n", name, why, chain[i+1])
		skipped = append(skipped, fmt.Sprintf("%s (exit %d, nothing written)", name, code))
		routedFrom, routeReason = name, why
		carried = handoff.Build(name, collected, nil)
	}
	return 1, nil
}

// truncateLabel keeps a label value inside the server's bound, on a rune
// boundary.
func truncateLabel(s string) string {
	const max = 256
	if len(s) <= max {
		return s
	}
	for i := max - len("…"); i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i] + "…"
		}
	}
	return ""
}

// promptFrom recovers the task from the user's own arguments: the last one,
// when it is not a flag. Agent flags do not travel — claude's headless mode is
// `-p <prompt>` where codex's is `exec <prompt>` — so a fallback is rebuilt
// from the prompt, and a run whose prompt cannot be found cannot be routed.
func promptFrom(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil // an interactive run: there is no prompt to carry
	}
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "-") {
		return last, nil
	}
	return "", fmt.Errorf("cannot route this run: a fallback agent needs the task re-expressed, and the last argument (%q) is a flag rather than a prompt.\n"+
		"  Put the prompt last, or drop --fallback for this run", last)
}

func fallbackArgv(d agents.Descriptor, prompt string, carried *handoff.Export, unattended bool) []string {
	if prompt == "" {
		return d.Command
	}
	if carried != nil {
		prompt = carried.Prompt(prompt)
	}
	if !unattended {
		return d.Console(prompt, false)
	}
	return d.Autonomous(prompt, nil)
}

// asksForAutonomy reports whether the user typed the agent's own
// skip-permissions flag — an explicit request to stop being asked.
func asksForAutonomy(agent string, args []string) bool {
	d, ok := agents.Lookup(agent)
	if !ok {
		return false
	}
	for _, flag := range d.SkipPermissionArgs {
		if slices.Contains(args, flag) {
			return true
		}
	}
	return false
}

func writeBriefing(ex *handoff.Export) func(context.Context, *api.Client, string) {
	return func(ctx context.Context, c *api.Client, sandbox string) {
		for name, data := range ex.Files {
			if err := c.WriteFile(ctx, sandbox, handoff.GuestDir+"/"+name, data); err != nil {
				fmt.Fprintf(os.Stderr, "sandbox-cli: the briefing could not be written (%v); %s starts from the prompt alone\n", err, sandbox)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: carrying %s's briefing forward — %d prompt(s), at %s\n", ex.From, ex.Turns, handoff.GuestDir)
	}
}

// readTranscript reads this run's conversation out of the sandbox. Best-effort
// by construction: an agent that died before writing one is the commonest case
// here. The contents come from the guest, so they are parsed as data (bounded
// by the protocol's read limit) and only ever quoted into a briefing for the
// next agent — never acted on by the host.
func readTranscript(ctx context.Context, c *api.Client, sandbox, agent string) []agentctx.Message {
	st, ok := transcriptStores[agent]
	if !ok {
		return nil
	}
	var best []agentctx.Message
	read, listed := 0, 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if listed++; listed > maxTranscriptDirs {
			return
		}
		entries, err := c.ListDir(ctx, sandbox, dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			// Names come from the guest: one carrying a slash or naming a
			// parent is not a directory entry this walk will follow.
			if e.Name == "" || e.Name == "." || e.Name == ".." || strings.Contains(e.Name, "/") {
				continue
			}
			if e.Type == "dir" && depth > 0 {
				walk(dir+"/"+e.Name, depth-1)
				continue
			}
			if e.Type != "file" || depth != 0 || !strings.HasPrefix(e.Name, st.prefix) || !strings.HasSuffix(e.Name, ".jsonl") {
				continue
			}
			if read++; read > maxTranscripts {
				return
			}
			data, err := c.ReadFile(ctx, sandbox, dir+"/"+e.Name)
			if err != nil {
				continue
			}
			msgs, err := st.parse(bytes.NewReader(data), 0)
			if err != nil || len(msgs) == 0 {
				continue
			}
			// The conversation that ended last is the one that failed.
			if best == nil || msgs[len(msgs)-1].At.After(best[len(best)-1].At) {
				best = msgs
			}
		}
	}
	walk(st.dir, st.depth)
	return best
}
