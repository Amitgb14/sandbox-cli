package fleet

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// lookupOrSkip fetches a descriptor, skipping rather than failing when the agent
// is not in the registry — these tests are about the fleet's handling of a
// descriptor, not about which agents the registry happens to carry today.
func lookupOrSkip(t *testing.T, name string) (agents.Descriptor, bool) {
	t.Helper()
	d, ok := agents.Lookup(name)
	if !ok {
		t.Skipf("no %s descriptor", name)
		return agents.Descriptor{}, false
	}
	return d, true
}

// The fleet owns no isolation policy: every task becomes the same sandbox.Options
// a `--worktree` run produces, with Detach set. That is a rule with teeth — it
// means every gate on the run path has to be repeated here — and it has been
// broken once already. `persist_auth` is a config key the run path consults
// before setting AuthPersistDir; BuildSpec mounts that directory whenever it is
// non-empty and does not re-check the config, so fleet setting it unconditionally
// made prod's "the refresh token is never mounted" true for `run` and false for
// `fleet`. ValidateProfile cannot catch that class: it validates the resolved
// Config, and the leak is in the Options.
//
// So the invariant is stated as a table over the *fields*, and the test fails
// when sandbox.Options grows one that is not in it. A new field is a new way for
// a fleet container to differ from the interactive container it is supposed to
// be identical to, and this is where that decision gets made rather than
// noticed later.

type fieldPolicy int

const (
	// fromSpec: the fleet file or the agent descriptor decides this. Its value is
	// the task talking, and the run path has an equivalent flag.
	fromSpec fieldPolicy = iota

	// gated: the fleet path may set it, but only after applying the same config
	// gate the run path applies. Each one needs its own test proving both
	// directions — see TestOptionsDoNotPersistAuthWhenTheProfileForbidsIt.
	gated

	// never: the fleet path must leave it zero. These are the fields that widen
	// what a container reaches or weaken how it is confined, and a fleet file has
	// no key for any of them on purpose — a task is a prompt and a branch, not a
	// place to ask for a docker socket.
	never
)

var optionsPolicy = map[string]fieldPolicy{
	// What the task is.
	"Project": fromSpec,
	"Command": fromSpec,
	"Branch":  fromSpec,
	"Verify":  fromSpec,
	// The task's own prompt, recorded as a label. fromSpec for the same reason
	// Verify is: it is the task talking, it widens nothing, and the run path has
	// the equivalent in the argv it builds.
	"Prompt": fromSpec,
	// A before-image of the workspace, recorded so this run's changes can be
	// told from what was already uncommitted. fromSpec: it widens nothing — a
	// commit id in a label grants no reach — and the run path records the same
	// thing for the same reason.
	"Baseline":    fromSpec,
	"Detach":      fromSpec,
	"RepoID":      fromSpec,
	"Agent":       fromSpec,
	"Base":        fromSpec,
	"Fleet":       fromSpec,
	"ExtraMounts": fromSpec, // the linked worktree's .git, without which the agent cannot commit
	"EnvAllow":    fromSpec, // the descriptor's names, forwarded only if the host has them
	"Env":         fromSpec, // the descriptor's own container settings (a keyring that is not there)
	"Memory":      fromSpec,
	"CPUs":        fromSpec,
	"Allow":       fromSpec,
	"Cache":       fromSpec,
	"GitIdentity": fromSpec,

	// The one gate, and the reason this file exists.
	"AuthPersistDir": gated,

	// Everything that would make a fleet container less confined than an
	// interactive one.
	"Image":       never, // the config's image, or none: a task does not choose what it runs in
	"Workdir":     never,
	"User":        never, // `--user root` contradicts the hardening a fleet inherits
	"Runtime":     never,
	"NoHardening": never, // an unattended run is the last place to drop cap-drop
	"Secrets":     never, // brokered values never travel through a file in the repository
	"Publish":     never, // publishing a port is asking for ingress; a fleet task has no reason to
	// A console is a keyboard for somebody who is going to attach. A fleet is
	// unattended by definition, and internal/agents only admits agents with a
	// verified headless mode for exactly this reason: an agent that stops to ask
	// permission does not fail, it hangs — holding a max_parallel slot until
	// somebody notices. never, so the fleet path leaves it zero.
	"Console":     never,
	"AddHosts":    never,
	"HostGateway": never, // reaching a host service is the opposite of what a fleet is for
	"TTY":         never, // nothing is attached; BuildSpec resolves this from Detach
	"NoMetrics":   never, // the live gauge is for foreground runs
}

// fleetOptions builds the Options for a task through the same path Launch uses,
// with a fleet file that sets everything a fleet file can set — so a `never`
// field found non-zero is one the fleet path is filling in on its own.
func fleetOptions(t *testing.T) sandbox.Options {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	spec := Spec{
		Agent:    "claude",
		Defaults: Defaults{Memory: "4g", CPUs: "2", Allow: []string{"example.com"}, Cache: true, Git: true},
		Tasks: []Task{{
			Branch: "feature-a",
			Prompt: "do it",
			Verify: "go test ./...",
			Memory: "8g",
			Allow:  []string{"docs.example.com"},
		}},
	}
	r := &Runner{Session: sandbox.New(config.Default()), Repo: "/repo", RepoID: testRepoID}
	agent, ok := lookupOrSkip(t, spec.AgentFor(spec.Tasks[0]))
	if !ok {
		return sandbox.Options{}
	}
	opts, err := r.options(spec, LaunchOptions{}, agent, spec.Tasks[0], t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	return opts
}

func TestOptionsPolicyCoversEveryField(t *testing.T) {
	typ := reflect.TypeOf(sandbox.Options{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if _, ok := optionsPolicy[name]; !ok {
			t.Errorf("sandbox.Options grew a field %q with no fleet policy.\n"+
				"Decide which it is and add it to optionsPolicy: fromSpec (the fleet file sets it),\n"+
				"gated (only after repeating the run path's config check — and add the test for it),\n"+
				"or never (the fleet path must leave it zero).", name)
		}
	}
	// The reverse, so a removed field does not leave a policy behind claiming to
	// protect something that no longer exists.
	for name := range optionsPolicy {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("optionsPolicy names %q, which sandbox.Options no longer has", name)
		}
	}
}

func TestFleetNeverWidensTheBoundary(t *testing.T) {
	opts := fleetOptions(t)
	v := reflect.ValueOf(opts)
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if optionsPolicy[name] != never {
			continue
		}
		if !v.Field(i).IsZero() {
			t.Errorf("fleet set %s = %v; a fleet container must be confined exactly as an interactive one,\n"+
				"and this field is one of the ways it could be less so", name, v.Field(i).Interface())
		}
	}
}

// The descriptor's container settings must reach a fleet container. The
// interactive wrapper applies them (droid's FACTORY_DISABLE_KEYRING is the
// standing example); a fleet that dropped them would send the agent looking for
// a keyring that is not there and, unattended, there is nobody to log in again.
func TestOptionsCarryTheDescriptorEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	agent, ok := lookupOrSkip(t, "droid")
	if !ok {
		return
	}
	if len(agent.Env) == 0 {
		t.Skip("droid no longer sets container env; nothing to check here")
	}
	r := &Runner{Session: sandbox.New(config.Default()), Repo: "/repo", RepoID: testRepoID}
	spec := Spec{Agent: "droid", Tasks: []Task{{Branch: "b", Prompt: "p"}}}
	opts, err := r.options(spec, LaunchOptions{}, agent, spec.Tasks[0], t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	for _, want := range agent.Env {
		if !contains(opts.Env, want) {
			t.Errorf("fleet dropped the descriptor's %q; the wrapper sets it and the fleet must too", want)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --share is how two fleet agents hand a file over, and it has to reach *every*
// task: a directory one agent can write and another cannot read is not a
// channel. It is a launch option rather than a fleet.yaml key on purpose — a
// cross-project mount stays something you type, not something a file that gets
// copied between repositories can turn on.
func TestShareMountReachesEveryTask(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	agent, ok := lookupOrSkip(t, "claude")
	if !ok {
		return
	}
	const mount = "/host/shared:/shared:rw"
	spec := Spec{Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: "p"}}}
	r := &Runner{Session: sandbox.New(config.Default()), Repo: "/repo", RepoID: testRepoID}
	wt := t.TempDir()

	opts, err := r.options(spec, LaunchOptions{ExtraMounts: []string{mount}}, agent, spec.Tasks[0], wt, "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	if !contains(opts.ExtraMounts, mount) {
		t.Errorf("the shared mount did not reach the task: %v", opts.ExtraMounts)
	}
	// And it must be *added* to the worktree's .git mount, not put in its place —
	// without that one the agent can edit files it can never commit.
	if want := len(sandbox.LinkedWorktreeMounts(wt)) + 1; len(opts.ExtraMounts) != want {
		t.Errorf("mounts = %v; the linked worktree mounts were displaced", opts.ExtraMounts)
	}
}

// The other direction: with no launch option, a fleet container has no mount
// beyond its own workspace and .git. Sharing is opt-in, and a fleet is where an
// always-on cross-project directory would be least noticed.
func TestFleetDoesNotShareByDefault(t *testing.T) {
	opts := fleetOptions(t)
	for _, m := range opts.ExtraMounts {
		if strings.Contains(m, ":/shared") {
			t.Errorf("a shared mount appeared without being asked for: %q", m)
		}
	}
}
