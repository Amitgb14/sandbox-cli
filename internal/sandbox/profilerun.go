package sandbox

import (
	"fmt"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// config.ValidateProfile asserts prod against the resolved **Config**. This
// asserts it against the resolved **run**, and the difference is a real gap
// rather than a belt-and-braces repeat.
//
// Several things prod names are set by flags that never reach the config at all.
// `--publish`, `--user`, `--memory`, `--cpus` and `--no-hardening` arrive in
// sandbox.Options and BuildSpec applies them over cfg, so a check that reads
// only cfg passes and the run still does the thing the profile forbids:
//
//	sandbox-cli run --profile prod --publish 0.0.0.0:8022:22 -- sleep inf
//
// succeeded, published the container on every interface, and had the entrypoint
// punch a matching hole in the default-deny INPUT chain — which is precisely
// what prod's own message ("publishing opens the boundary inward") promises
// cannot happen.
//
// It lives in BuildSpec rather than in the CLI because that is where every
// caller converges. fleet and studioapi build Options directly, and CLAUDE.md's
// rule with teeth is that every gate on the run path must be repeated by every
// caller that builds them — a rule better satisfied structurally than by three
// callers remembering. This is the same reasoning that puts the kernel-boundary
// demand on spec.Runtime rather than in ValidateProfile.
//
// It checks only what a flag can change. Everything else prod asserts is already
// established at load time, and repeating it here would be two sources of truth
// for one rule.
func enforceProfileOnRun(cfg config.Config, opts Options) error {
	if cfg.Profile != config.ProfileProd {
		return nil
	}
	var bad []string
	if len(opts.Publish) > 0 {
		bad = append(bad, fmt.Sprintf("--publish %v is refused (publishing opens the boundary inward)", opts.Publish))
	}
	if config.IsRootUser(opts.User) {
		bad = append(bad, fmt.Sprintf("--user %q is refused (prod does not run the agent as root)", opts.User))
	}
	// "0" is docker's spelling of "no limit", so it defeats the bound rather than
	// setting one — the same reason the config check requires these to be set.
	if isUncapped(opts.Memory) {
		bad = append(bad, "--memory 0 is refused (a runaway agent must not take a shared host down)")
	}
	if isUncapped(opts.CPUs) {
		bad = append(bad, "--cpus 0 is refused (a runaway agent must not take a shared host down)")
	}
	if opts.NoHardening {
		bad = append(bad, "--no-hardening is refused (it drops cap-drop, no-new-privileges and the pids limit)")
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("this run does not satisfy the %q profile:\n  - %s\n"+
		"  a flag cannot widen what the profile guarantees; use --profile dev if that is what you want",
		config.ProfileProd, strings.Join(bad, "\n  - "))
}

// isUncapped reports whether a resource flag asks for no limit. Empty means "not
// given", which leaves the profile's own value in place and is fine.
func isUncapped(v string) bool { return v == "0" }
