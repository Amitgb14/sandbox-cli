package session

import (
	"fmt"
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// OptionsFor turns a spawn request into `sandbox.Options`.
//
// This is the fourth thing in the tool that builds Options — after `internal/cli`,
// `internal/fleet` and `internal/studioapi` — and that is the whole reason it is
// written the way it is. `internal/fleet`'s rule with teeth says every gate on the
// run path must be repeated by every caller that builds Options, and
// `gates_test.go` exists because that rule was broken once: `BuildSpec` mounts
// `AuthPersistDir` whenever it is non-empty without re-checking the config, so
// prod's "the refresh token is never mounted" held for `run` and not for `fleet`.
// A builder fed by a *request* rather than by flags is the most exposed of the four.
//
// Two things keep it honest, and neither is a comment.
//
// **The params are narrow.** `protocol.PaneSpawnParams` has no `mounts`, `secrets`,
// `env`, `env_allow`, `user`, `image`, `runtime` or `no_hardening` — the fields
// `config/trust.go` refuses from a project file. A caller here can ask for a
// sandbox and cannot ask for a different *kind* of sandbox, so the dangerous half
// of the surface does not exist to be gated.
//
// **Every remaining field is classified and tested.**
// `TestSpawnParamsAreAllClassified` fails when the struct grows one that has not
// been decided on, and `TestOptionsForRepeatsTheRunPathGates` checks the gates this
// function repeats in both directions.
//
// The config is the caller's resolved one, never loaded here: which config is in
// force is a decision made where the daemon started, and a builder that reloaded it
// could answer to a different file than the one `serve` reported at startup.
// workDir is the directory the pane runs in — the worktree when one was asked for,
// the repository root otherwise — and repoRoot is the repository it belongs to. The
// two differ for a worktree run, and conflating them is how a worktree pane comes to
// be labelled with the main checkout's branch.
func OptionsFor(cfg config.Config, repoRoot, workDir, repoID string, p protocol.PaneSpawnParams) (sandbox.Options, error) {
	if p.Kind == "" {
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"a pane needs a kind: one of agent, shell, command, verify, console")
	}
	if p.Agent == "" && len(p.Argv) == 0 {
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"nothing to run: name an agent, or give an argv")
	}
	if p.Agent != "" && len(p.Argv) > 0 {
		// Refused rather than resolved. An agent's argv is built by its descriptor,
		// and a caller supplying one would be choosing flags the descriptor
		// deliberately does not pass — `--dangerously-skip-permissions` above all,
		// which is why `SkipPermissions` is a separate, gated field.
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"agent and argv are two answers to one question: an agent's command line comes from its descriptor")
	}
	if p.Resume != "" && !p.Console {
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"resuming a conversation needs a console: a headless resume replays one prompt into an old conversation and exits")
	}
	if p.Console && p.Verify != "" {
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"console and verify are refused together: verify's exit code is the answer it exists to give, and an interactive session's exit code is whenever somebody quit")
	}

	opts := sandbox.Options{
		Project: workDir,
		RepoID:  repoID,
		// Always. A socket is an HTTP-shaped thing with nowhere to hold a pty, and
		// `Console` is the separate claim that the *container* keeps one.
		Detach:      true,
		Console:     p.Console,
		Verify:      p.Verify,
		Prompt:      p.Prompt,
		GitIdentity: p.Git,
	}

	// The branch, the base and the three binds a linked worktree needs, all from the
	// same helpers the run path uses.
	//
	// None of this was here at first, and a cross-package test against
	// `run --detach --dry-run` is what found it — the session package's own socket
	// test could not, because both its sides went through this function. Three real
	// divergences, and the third was the worst:
	//
	//   - **Branch** was taken from `p.Worktree`, so a spawn with no worktree had no
	//     branch label and therefore a *timestamped* container name instead of
	//     `sandbox-<repo>-<branch>`. That name is the one-agent-per-branch lock, so the
	//     socket path silently had no lock.
	//   - **Base** was taken from the request only, so the label `fleet land` reads to
	//     decide what to merge into was usually empty.
	//   - **LinkedWorktreeMounts** was missing entirely. A linked worktree's `.git` is
	//     a pointer file, so without the parent's `.git` bound in, git inside the
	//     container cannot read the repository at all — the agent can edit files and
	//     not commit them.
	//
	// Read from the directory rather than from the request, because the repository is
	// the authority on which branch is checked out there and a request asserting one
	// would be asserting something it cannot know.
	opts.ExtraMounts = append(opts.ExtraMounts, sandbox.LinkedWorktreeMounts(workDir)...)
	opts.Branch = worktree.Branch(workDir)
	opts.Base = p.Base
	if opts.Base == "" {
		// The branch the main checkout is sitting on, which is what this work is
		// expected to land on. Best-effort: a repository that cannot be read has no
		// base to stamp, which is not an error.
		opts.Base = worktree.Branch(repoRoot)
	}

	// --- the gates, each repeating a rule the run path applies -----------------

	// Caps **replace** the configured value, which is what `--memory` does on the
	// flag path and what a fleet task does with `defaults:` — and the consistency is
	// the argument. A narrow-only rule here would be one nothing else in the tool
	// has, and it would need a size comparator to enforce, which is a parser with its
	// own bugs standing between a caller and a number the engine already validates.
	//
	// Defensible because a cap is resource sanity rather than a boundary control —
	// `checkCapacity` says the same of the fleet's memory check — and because the
	// socket's authority is same-uid: a caller who can open it can run `docker run
	// --memory` directly. What a cap must never become is a way to reach *further*,
	// and a number cannot.
	if p.Memory != "" {
		opts.Memory = p.Memory
	}
	if p.CPUs != "" {
		opts.CPUs = p.CPUs
	}

	// Egress may only be added to, which is `fleet.yaml`'s asymmetry and has the
	// same reason: a caller that could subtract would be asking for a narrower
	// allowlist than the user wrote, and the way to want less egress is
	// `network: none`.
	opts.Allow = append([]string(nil), p.Allow...)
	// The *posture* is not here, because `sandbox.Options` has no field for it: the
	// mode lives on the config, and `opts.Allow` can only add domains. A request
	// asking for `none` is therefore applied by `EffectiveConfig` before this runs,
	// which is also where its tighten-only rule is enforced — see that function.

	// prod refuses published ports, and there is no Publish field for a caller to
	// try: publishing opens the boundary *inward*, and a request on a socket is
	// not the place to ask for it. Named here so the absence reads as a decision.

	// Sharing is a boolean plus a name, never a path. The directory is the
	// daemon's own, created and vetted by the same helper the run path uses.
	if p.Share {
		m, err := sandbox.ShareMount(p.ShareName)
		if err != nil {
			return sandbox.Options{}, err
		}
		opts.ExtraMounts = append(opts.ExtraMounts, m.Mount)
	} else if p.ShareName != "" {
		return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
			"share_name names a subdirectory of the shared mount, and share is not set — there is nothing for it to scope")
	}

	// --- the agent, and the one gate that was missed once ---------------------

	if p.Agent != "" {
		agent, ok := agents.Lookup(p.Agent)
		if !ok {
			// Only agents with a verified headless mode are in the table, which is
			// `internal/agents`' rule: an unattended agent that stops to ask does not
			// fail, it hangs.
			return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
				"no verified adapter for agent %q", p.Agent)
		}
		opts.Agent = agent.Name
		opts.EnvAllow = agent.EnvAllow
		opts.Env = append(opts.Env, agent.Env...)

		switch {
		case p.Console && p.Resume != "":
			args, ok := resumeArgsFor(agent.Name)
			if !ok {
				return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
					"agent %q has no verified resume flag", agent.Name)
			}
			opts.Command = concat(agent.Console("", p.SkipPermissions), args, []string{p.Resume})
			// Recorded, so the conversation belonging to this run is known rather
			// than inferred: a resumed session began before its container, which
			// every correlation heuristic assumes cannot happen.
			opts.SessionID = p.Resume
		case p.Console:
			if p.Prompt != "" && !agent.CanSeedConsole() {
				return sandbox.Options{}, protocol.Errorf(protocol.CodeInvalid,
					"%s cannot be given a prompt for an interactive session: it has no way to be seeded on the command line",
					agent.Name)
			}
			opts.Command = agent.Console(p.Prompt, p.SkipPermissions)
		default:
			opts.Command = sandbox.WithVerify(agent.Autonomous(p.Prompt, nil), p.Verify)
		}

		// The gate `gates_test.go` exists for. The default auth path is an OAuth
		// refresh token in this directory, readable by the agent, and prod turns
		// persist_auth off so there is nothing there to steal — but `BuildSpec`
		// mounts `AuthPersistDir` whenever it is non-empty and does not re-check
		// the config, so the check belongs on every caller that builds Options.
		// There is no field for a request to ask for it: it is the config's answer
		// or nothing.
		if cfg.PersistAuthEnabled() {
			if dir := config.AgentStateDir(agent.PersistDir); dir != "" {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					return sandbox.Options{}, fmt.Errorf("creating auth persist dir %s: %w", dir, err)
				}
				opts.AuthPersistDir = dir
			}
		}
	} else {
		opts.Command = sandbox.WithVerify(append([]string(nil), p.Argv...), p.Verify)
	}

	return opts, nil
}

// EffectiveConfig applies the parts of a request that are properties of the
// *configuration* rather than of the run, and refuses the ones that would loosen it.
//
// Only the network posture today, and it has to be here rather than in `OptionsFor`
// for a structural reason worth recording: `sandbox.Options` has no network field.
// `opts.Allow` adds domains and turns the allowlist on; the mode itself —
// `default` / `allowlist` / `none` — is read off the config by `BuildSpec`. So a
// request asking for `none` is a request to run under a stricter config, and this is
// where that is granted or refused.
//
// Tighten only, in the order `config/trust.go` uses for a project file:
// `default` → `allowlist` → `none`. A request arriving on a socket has exactly the
// authority a checked-in file has — none to widen — and the reason is the same: the
// narrowest thing anybody asked for is the thing that should be in force.
func EffectiveConfig(cfg config.Config, p protocol.PaneSpawnParams) (config.Config, error) {
	if p.Network == "" {
		return cfg, nil
	}
	mode, err := tightenNetwork(cfg, p.Network)
	if err != nil {
		return config.Config{}, err
	}
	cfg.Network.Mode = mode
	return cfg, nil
}

// resumeArgsFor is the agent's verified resume flag, from the store table rather than
// written here — the same rule `cli/recover_resume.go` and `studioapi` keep, because
// the flag is a fact about the agent and a third spelling of it is a third chance to
// be wrong.
func resumeArgsFor(agent string) ([]string, bool) {
	store, ok := agentctx.Lookup(agent)
	if !ok || len(store.Resume) == 0 {
		return nil, false
	}
	return store.Resume, true
}

// ResolveWorktreeFor creates or finds the worktree a spawn asked for, and reports
// the directory to run in.
//
// Separate from OptionsFor because it *writes* — `worktree.Resolve` creates a
// checkout — and a builder that is safe to call twice is easier to test than one
// that is not.
func ResolveWorktreeFor(repoRoot string, p protocol.PaneSpawnParams) (dir string, err error) {
	if p.Worktree == "" {
		return repoRoot, nil
	}
	info, err := worktree.Resolve(repoRoot, p.Worktree)
	if err != nil {
		return "", err
	}
	return info.Path, nil
}

// tightenNetwork accepts a posture only when it is at least as strict as the one in
// force.
//
// The same direction rule `config/trust.go` applies to a project file —
// `default` → `allowlist` → `none`, tighten only — because a request arriving on a
// socket has exactly the authority a checked-in file has: none to loosen.
func tightenNetwork(cfg config.Config, want string) (string, error) {
	rank := map[string]int{"default": 0, "": 0, "allowlist": 1, "none": 2}
	have, ok := rank[cfg.Network.Mode]
	if !ok {
		have = 0
	}
	asked, ok := rank[want]
	if !ok {
		return "", protocol.Errorf(protocol.CodeInvalid,
			"network %q: want allowlist or none", want)
	}
	if asked < have {
		return "", protocol.Errorf(protocol.CodePolicyRefused,
			"network %q would loosen what is in force (%q): a request may tighten the boundary and never widen it",
			want, cfg.Network.Mode)
	}
	return want, nil
}

func concat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// paneKindFromParams is what the pane is recorded as, which must agree with what the
// options say rather than with what the request claimed.
//
// A request naming `kind: agent` while setting a verify would otherwise produce a
// pane labelled `agent` whose exit code is a verdict — the label and the container
// disagreeing about what it is, which is the thing labels exist to prevent.
func paneKindFromParams(p protocol.PaneSpawnParams) protocol.PaneKind {
	switch {
	case p.Verify != "":
		return protocol.PaneVerify
	case p.Agent == "":
		return protocol.PaneCommand
	case p.Console:
		return protocol.PaneConsole
	default:
		return protocol.PaneAgent
	}
}
