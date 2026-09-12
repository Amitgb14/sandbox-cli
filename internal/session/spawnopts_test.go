package session

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// This file is the reason `pane.spawn` is safe to have.
//
// `OptionsFor` is the fourth thing in the tool that builds `sandbox.Options`, and the
// only one fed by a *request* rather than by flags. `internal/fleet/gates_test.go`
// exists because the "every caller repeats every gate" rule was broken once —
// `persist_auth` — and that table is the shape of the answer: classify every field,
// fail when the struct grows one nobody decided on.
//
// The same table, for the params instead of the Options.

type paramPolicy int

const (
	// honoured: the request's value is used as given. These widen nothing — a
	// prompt, a branch, a kind — or are narrowed by the engine rather than by us.
	honoured paramPolicy = iota

	// gated: the request may ask, and something checks before it is granted. Each
	// needs a test proving both directions, below.
	gated

	// derived: not taken from the request at all. The field exists on the wire
	// because a client names it, and what reaches Options is computed — so a
	// request cannot assert it.
	derived
)

// Every field of protocol.PaneSpawnParams, and what this builder does with it.
//
// What is *not* in this table is the other half of the design: there is no `mounts`,
// `secrets`, `env`, `env_allow`, `user`, `image`, `runtime` or `no_hardening` field to
// classify, because `PaneSpawnParams` does not have them. Those are the keys
// `config/trust.go` refuses from a project file, and a socket request has the same
// authority a checked-in file has: none to widen. The dangerous surface does not
// exist rather than being guarded.
var paramPolicies = map[string]paramPolicy{
	// What to run. None of these reaches past the container.
	//
	// Kind is `derived` and not `honoured`, which the first version of this row got
	// wrong: `paneKindFromParams` computes the recorded kind from the *options*, so a
	// request claiming `agent` while setting a verify cannot produce a pane labelled
	// agent whose exit code is a verdict. The one exception is `shell`, which nothing
	// else can tell — `argv: ["bash"]` with a console is indistinguishable from any
	// other command — so that value is carried through. An inaccurate row here is
	// worse than a missing one, because this table is what the next reader trusts.
	"Kind":     derived,
	"Prompt":   honoured,
	"Worktree": honoured,
	"Base":     honoured,
	"Verify":   honoured,
	"Git":      honoured, // forwards the git identity, which the run path's --git does
	"DryRun":   honoured,

	// Caps. Honoured rather than gated, and the reasoning is in OptionsFor: a cap
	// replaces on the flag path and in fleet, a narrow-only rule here would be one
	// nothing else has, and a number cannot reach further.
	"Memory": honoured,
	"CPUs":   honoured,

	// Adds egress domains and cannot subtract — fleet.yaml's asymmetry.
	"Allow": honoured,

	// Gated, each against a rule the run path applies.
	"Agent":   gated, // only agents with a verified headless adapter
	"Argv":    gated, // refused together with Agent: a descriptor owns its argv
	"Network": gated, // tighten only, applied by EffectiveConfig
	"Share":   gated, // a boolean and a name, never a path
	"Resume":  gated, // requires Console, and a verified resume flag
	"Console": gated, // refused together with Verify

	// ShareName is only meaningful with Share, and says so rather than being
	// silently ignored.
	"ShareName": gated,

	// SkipPermissions reaches the agent's own argv, and only for a console run —
	// a headless run gets the flag from Descriptor.Autonomous regardless, so the
	// request cannot change what a headless run does.
	"SkipPermissions": gated,
}

func TestSpawnParamsAreAllClassified(t *testing.T) {
	typ := reflect.TypeOf(protocol.PaneSpawnParams{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := paramPolicies[name]; !ok {
			t.Errorf("protocol.PaneSpawnParams grew a field %q with no policy.\n"+
				"  Decide what OptionsFor does with it and add it here: honoured (used as given, widens nothing),\n"+
				"  gated (checked first — and add the test for it), or derived (not taken from the request).\n"+
				"  A new field is a new way for a socket-spawned container to differ from the one the CLI\n"+
				"  would have started, and this is where that decision gets made rather than noticed later.", name)
		}
	}
	for name := range paramPolicies {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("paramPolicies names %q, which PaneSpawnParams no longer has", name)
		}
	}
}

// The gate the whole table exists for, in both directions.
//
// The default auth path is an OAuth refresh token in the persisted HOME, readable by
// the agent, and prod turns `persist_auth` off so there is nothing there to steal.
// `BuildSpec` mounts `AuthPersistDir` whenever it is non-empty and does *not*
// re-check the config — which is how prod's promise held for `run` and not for
// `fleet`. There is no request field for it: the config answers, or nothing does.
func TestOptionsForRepeatsThePersistAuthGate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	p := protocol.PaneSpawnParams{Kind: protocol.PaneAgent, Agent: "claude", Prompt: "go"}

	on := config.Default()
	yes := true
	on.PersistAuth = &yes
	opts, err := OptionsFor(on, "/repo", "/repo", "repo-1234", p)
	if err != nil {
		t.Fatal(err)
	}
	if opts.AuthPersistDir == "" {
		t.Error("persist_auth enabled and no AuthPersistDir: the agent would have to log in on every run")
	}

	off := config.Default()
	no := false
	off.PersistAuth = &no
	opts, err = OptionsFor(off, "/repo", "/repo", "repo-1234", p)
	if err != nil {
		t.Fatal(err)
	}
	if opts.AuthPersistDir != "" {
		t.Errorf("persist_auth disabled and AuthPersistDir = %q: that directory holds the refresh token, and this is the leak gates_test exists for", opts.AuthPersistDir)
	}
}

// A request may tighten the network and never loosen it — the direction rule
// `config/trust.go` applies to a project file, because a socket request has the same
// authority: none to widen.
func TestNetworkMayOnlyTighten(t *testing.T) {
	cases := []struct {
		have, want string
		ok         bool
	}{
		{"allowlist", "none", true},      // stricter
		{"default", "allowlist", true},   // stricter
		{"none", "none", true},           // same
		{"none", "allowlist", false},     // looser
		{"allowlist", "default", false},  // looser
		{"allowlist", "whatever", false}, // not a posture
	}
	for _, tc := range cases {
		cfg := config.Default()
		cfg.Network.Mode = tc.have
		got, err := EffectiveConfig(cfg, protocol.PaneSpawnParams{Network: tc.want})
		if tc.ok {
			if err != nil {
				t.Errorf("in force %q, asked %q: %v", tc.have, tc.want, err)
				continue
			}
			if got.Network.Mode != tc.want {
				t.Errorf("in force %q, asked %q: mode = %q", tc.have, tc.want, got.Network.Mode)
			}
			continue
		}
		if err == nil {
			t.Errorf("in force %q, asked %q: accepted, and it loosens", tc.have, tc.want)
		}
	}

	// And no request at all leaves the config exactly as it was.
	cfg := config.Default()
	cfg.Network.Mode = "allowlist"
	got, err := EffectiveConfig(cfg, protocol.PaneSpawnParams{})
	if err != nil || got.Network.Mode != "allowlist" {
		t.Errorf("no network asked for: mode = %q (%v)", got.Network.Mode, err)
	}
}

// The refusals. Each is a pair the run path or the daemon already refuses, stated
// here so a request cannot produce a container the rest of the tool would not have
// built.
func TestOptionsForRefusals(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	cfg := config.Default()

	cases := []struct {
		name   string
		params protocol.PaneSpawnParams
		want   string
	}{
		{"no kind", protocol.PaneSpawnParams{Agent: "claude"}, "want one of"},
		// Checked against the set rather than for emptiness: a typo was accepted and
		// then silently replaced by a derived kind, so a client could not tell a
		// mistake from a decision.
		{"a kind that is not one", protocol.PaneSpawnParams{
			Kind: protocol.PaneKind("not-a-kind"), Agent: "claude", Prompt: "go",
		}, "want one of"},
		// Three fields that only mean something to an agent. Refused rather than
		// ignored — dropping `resume` silently lost the conversation id, so the pane's
		// state was then decided with no transcript to correlate against.
		{"console with no agent", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"bash"}, Console: true,
		}, "console needs an agent"},
		{"resume with no agent", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"bash"}, Console: true, Resume: "abc",
		}, "needs an agent"},
		{"skip_permissions with no agent", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"bash"}, SkipPermissions: true,
		}, "skip_permissions needs an agent"},
		// A resume lands on the agent's command line after the descriptor's flag, so a
		// value shaped like a flag is the thing refusing agent+argv exists to prevent.
		{"a resume that is a flag", protocol.PaneSpawnParams{
			Kind: protocol.PaneConsole, Agent: "claude", Console: true,
			Resume: "--mcp-config /workspace/x.json",
		}, "not a session id"},
		{"nothing to run", protocol.PaneSpawnParams{Kind: protocol.PaneAgent}, "nothing to run"},
		// An agent's argv is its descriptor's, and a caller supplying one would be
		// choosing flags the descriptor deliberately does not pass.
		{"agent and argv", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "claude", Argv: []string{"sh"},
		}, "two answers to one question"},
		{"resume with no console", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "claude", Resume: "abc",
		}, "needs a console"},
		{"console with verify", protocol.PaneSpawnParams{
			Kind: protocol.PaneConsole, Agent: "claude", Console: true, Verify: "go test ./...",
		}, "refused together"},
		{"an agent with no verified adapter", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "not-an-agent", Prompt: "go",
		}, "no verified adapter"},
		// share_name without share: said rather than silently ignored, because a
		// caller that asked to scope a mount and got the root would not know.
		{"share_name with no share", protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"true"}, ShareName: "x",
		}, "share is not set"},
	}
	for _, tc := range cases {
		_, err := OptionsFor(cfg, "/repo", "/repo", "repo-1234", tc.params)
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v\n  want a refusal mentioning %q", tc.name, err, tc.want)
		}
	}
}

// A socket spawn is always detached, and the pane's recorded kind follows the
// *options* rather than the request's claim.
//
// A request naming `kind: agent` while setting a verify would otherwise produce a
// pane labelled `agent` whose exit code is a verdict — the label and the container
// disagreeing about what the thing is, which is what labels exist to prevent.
func TestSpawnIsDetachedAndTheKindFollowsTheOptions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	cfg := config.Default()

	opts, err := OptionsFor(cfg, "/repo", "/repo", "repo-1234", protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Detach {
		t.Error("a socket spawn is not detached; a socket has nowhere to hold a pty")
	}

	for _, tc := range []struct {
		name   string
		params protocol.PaneSpawnParams
		want   protocol.PaneKind
	}{
		{"a verify outranks the claimed kind", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "claude", Verify: "go test ./...",
		}, protocol.PaneVerify},
		{"a console run", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "claude", Console: true,
		}, protocol.PaneConsole},
		{"no agent is a command", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Argv: []string{"true"},
		}, protocol.PaneCommand},
		{"an agent", protocol.PaneSpawnParams{
			Kind: protocol.PaneAgent, Agent: "claude", Prompt: "go",
		}, protocol.PaneAgent},
	} {
		if got := paneKindFromParams(tc.params); got != tc.want {
			t.Errorf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The verify wrapper is the one from `internal/sandbox`, which is the one
// `fleet status` and `fleet land` read `VerifyFailedExit` back from.
//
// It moved there when this package became its third caller: `session` is imported by
// `fleet`, so reaching back would be an import cycle, and the alternative was a second
// copy of a script whose exit code is a contract read off containers that exited days
// ago.
func TestVerifyWrapsThroughTheSharedImplementation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	opts, err := OptionsFor(config.Default(), "/repo", "/repo", "repo-1234", protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"npm", "test"}, Verify: "test -f built",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := sandbox.WithVerify([]string{"npm", "test"}, "test -f built")
	if !reflect.DeepEqual(opts.Command, want) {
		t.Errorf("command = %v\n  want %v", opts.Command, want)
	}
	// And the argv goes through "$@" rather than being pasted into the script, since
	// it carries the prompt.
	joined := strings.Join(opts.Command, " ")
	if !strings.Contains(joined, `"$@"`) {
		t.Errorf("the wrapper does not pass the argv through \"$@\":\n%s", joined)
	}
}

// The high finding from the review of this op, and the reason its "tighten only"
// claim did not hold.
//
// `BuildSpec` computes `allowlist := cfg.Network.Mode == "allowlist" ||
// len(opts.Allow) > 0`, so a non-empty `allow` switched the allowlist *on* — turning
// a `network: none` config into a bridged container running the root firewall phase
// with the **baseline** egress list, github.com included, plus whatever the caller
// named. Measured before the fix: a request that explicitly asked for `network: none`
// still came out with `--network sandbox-cli --user root --cap-add NET_ADMIN`.
//
// `config/load.go` guards the CLI's `--allow` the same way, which is where the intent
// was already written down.
func TestAllowCannotLoosenTheNetwork(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	cfg := config.Default()
	cfg.Network.Mode = "none"

	for _, name := range []string{"implicitly", "while explicitly asking for none"} {
		p := protocol.PaneSpawnParams{
			Kind: protocol.PaneCommand, Argv: []string{"true"}, Allow: []string{"evil.example"},
		}
		if name != "implicitly" {
			p.Network = "none"
		}
		eff, err := EffectiveConfig(cfg, p)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateSpawn(eff, p); err == nil {
			t.Errorf("%s: a request naming egress domains under `none` was accepted", name)
		} else if !strings.Contains(err.Error(), "reaches nothing") {
			t.Errorf("%s: %v\n  want a refusal about the posture", name, err)
		}
	}

	// And under a posture that *can* carry an allowlist, the domains go through.
	open := config.Default()
	open.Network.Mode = "allowlist"
	opts, err := OptionsFor(open, "/repo", "/repo", "r-1", protocol.PaneSpawnParams{
		Kind: protocol.PaneCommand, Argv: []string{"true"}, Allow: []string{"docs.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.Allow) != 1 || opts.Allow[0] != "docs.example.com" {
		t.Errorf("allow = %v under allowlist, want it honoured", opts.Allow)
	}
}

// Every refusal comes before the first side effect, which is `worktree.Resolve`'s own
// rule and was inverted here: a malformed request was refused *after* its worktree had
// been created, so repeated bad requests accumulated branches.
//
// `ValidateSpawn` is pure, and this is what pins that — a refusal that needed the
// worktree in order to fire would fail here.
func TestValidateSpawnTouchesNothing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	dir, _ := repo(t)

	// The request the review used: contradictory, and naming a worktree.
	bad := protocol.PaneSpawnParams{
		Kind: protocol.PaneAgent, Agent: "claude", Argv: []string{"sh"}, Worktree: "junk-branch",
	}
	if err := ValidateSpawn(config.Default(), bad); err == nil {
		t.Fatal("the contradictory request was accepted")
	}

	// Nothing was created: no branch, no worktree.
	if wts, err := worktree.List(dir); err == nil {
		for _, wt := range wts {
			if wt.Branch == "junk-branch" {
				t.Errorf("validation created the worktree for %q", wt.Branch)
			}
		}
	}
	if _, exists, _ := worktree.Path(dir, "junk-branch"); exists {
		t.Error("validation created a worktree directory for a request it refused")
	}
}

// `shell` is the one kind the request is the only thing that knows, so it survives —
// and a verify still outranks whatever the request called the pane, because the label
// and the container must not disagree about what the thing is.
func TestShellKindSurvivesAndVerifyOutranks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params protocol.PaneSpawnParams
		want   protocol.PaneKind
	}{
		{"a shell", protocol.PaneSpawnParams{
			Kind: protocol.PaneShell, Argv: []string{"bash"},
		}, protocol.PaneShell},
		{"a verify outranks even a shell", protocol.PaneSpawnParams{
			Kind: protocol.PaneShell, Argv: []string{"bash"}, Verify: "true",
		}, protocol.PaneVerify},
	} {
		if got := paneKindFromParams(tc.params); got != tc.want {
			t.Errorf("%s: kind = %q, want %q", tc.name, got, tc.want)
		}
	}
}
