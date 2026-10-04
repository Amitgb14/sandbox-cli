package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
)

// A routed run is a wrapper run with somewhere to fall through to:
// `--fallback codex`, or `routing: [claude, codex]` in the user's config. Each
// agent's provider is probed before a sandbox is created for it, and one that
// is down is skipped for the next.
//
// A run that started is never retried with another agent. Retrying is safe
// only for a run that failed having changed nothing, and that was bring-back's
// answer: with no repository there is nothing to compare, and a run that may
// have done work must not be done twice.

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
			rs.argv = fallbackArgv(d, prompt, unattended)
		}
		return execute(ctx, rf, rs)
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

func fallbackArgv(d agents.Descriptor, prompt string, unattended bool) []string {
	if prompt == "" {
		return d.Command
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
