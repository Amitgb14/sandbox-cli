package cli

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/handoff"
)

// What a fallback attempt is built with.
//
// This file exists because every mistake retarget can make is *silent*. A
// missing briefing mount, a dropped FACTORY_DISABLE_KEYRING, the primary's
// history mount carried into another agent's container — none of them fail a
// run. The container starts, the agent runs, and it is simply missing something
// nobody sees until they read the transcript. A review found three of them at
// once, in code that had tests: the tests all asked which *agent* was chosen and
// none asked what it was handed.

// The claude wrapper's own additions, spelled as it spells them, so a change to
// what a wrapper mounts shows up here as a test that stops describing reality.
const (
	claudesHistory  = "/home/me/.claude/projects/-home-me-repo:/sandbox/home/.claude/projects/-workspace:rw"
	claudesSettings = "/tmp/managed-settings.json:/etc/claude-code/managed-settings.json:ro"
)

// primaryRun is a claude run as the wrapper leaves it: the user's own inputs
// plus everything claude.go added on top.
func primaryRun(t *testing.T) (runFlags, userInputs) {
	t.Helper()
	claude, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("claude is not in the descriptor table")
	}
	user := userInputs{
		mounts:   []string{"/home/me/notes:/notes:ro"},
		env:      []string{"TERM=xterm-256color"},
		envAllow: []string{"MY_OWN_TOKEN"},
	}
	rf := runFlags{
		project:     "/home/me/repo",
		persistName: claude.PersistDir,
		mounts:      append(append([]string(nil), user.mounts...), claudesHistory, claudesSettings),
		env:         append(append([]string(nil), user.env...), claude.Env...),
		envAllow:    append(append([]string(nil), user.envAllow...), claude.EnvAllow...),
	}
	return rf, user
}

func TestFallbackIsBuiltFromItsOwnDescriptor(t *testing.T) {
	rf, user := primaryRun(t)
	codex, ok := agents.Lookup("codex")
	if !ok {
		t.Fatal("codex is not in the descriptor table")
	}
	briefing := &handoff.Export{Dir: t.TempDir()}

	got := retarget(rf, codex, user, briefing)

	// Its own login. Sharing claude's persisted HOME would have codex looking for
	// credentials in a directory belonging to the agent that just failed.
	if got.persistName != codex.PersistDir {
		t.Errorf("persistName = %q, want codex's %q", got.persistName, codex.PersistDir)
	}

	// The briefing, mounted where the prompt says it is. This is the one the
	// review found: handoff.GuestDir appeared only in the message telling the
	// agent where to read, so every failover pointed at a path that did not exist.
	want := briefing.Dir + ":" + handoff.GuestDir + ":ro"
	if !slices.Contains(got.mounts, want) {
		t.Errorf("briefing not mounted\n got %q\nwant it to contain %q", got.mounts, want)
	}

	// The user's own mounts survive; claude's do not.
	if !slices.Contains(got.mounts, user.mounts[0]) {
		t.Errorf("the user's own mount was dropped: %q", got.mounts)
	}
	for _, m := range []string{claudesHistory, claudesSettings} {
		if slices.Contains(got.mounts, m) {
			t.Errorf("claude's own mount travelled to codex: %q", m)
		}
	}
	if len(got.mounts) != len(user.mounts)+1 {
		t.Errorf("mounts = %q, want exactly the user's plus the briefing", got.mounts)
	}

	// Likewise the allowlist: the user's names, plus this agent's, and not the
	// last one's. A forwarded ANTHROPIC_API_KEY is a credential in a container
	// that has no use for it.
	if !slices.Contains(got.envAllow, user.envAllow[0]) {
		t.Errorf("the user's own --env-allow was dropped: %q", got.envAllow)
	}
	for _, name := range codex.EnvAllow {
		if !slices.Contains(got.envAllow, name) {
			t.Errorf("codex's %s is not forwarded: %q", name, got.envAllow)
		}
	}
	claude, _ := agents.Lookup("claude")
	for _, name := range claude.EnvAllow {
		if slices.Contains(codex.EnvAllow, name) {
			continue // legitimately both agents' — nothing to say
		}
		if slices.Contains(got.envAllow, name) {
			t.Errorf("claude's %s is still forwarded to codex: %q", name, got.envAllow)
		}
	}

	// No briefing yet — the first failover of a run whose agent died before
	// writing a transcript — mounts nothing rather than an empty string.
	none := retarget(rf, codex, user, nil)
	if len(none.mounts) != len(user.mounts) {
		t.Errorf("without a briefing, mounts = %q, want just the user's", none.mounts)
	}
}

// The container variables a descriptor sets are not decoration: droid without
// FACTORY_DISABLE_KEYRING looks for a keyring the container does not have, which
// is the unattended-login failure internal/agents was written to prevent. A
// fallback that inherited the primary's Env would hit it every time.
func TestFallbackGetsTheContainerEnvItsDescriptorSets(t *testing.T) {
	rf, user := primaryRun(t)
	for _, name := range agents.Names() {
		d, _ := agents.Lookup(name)
		if len(d.Env) == 0 {
			continue
		}
		got := retarget(rf, d, user, nil)
		for _, kv := range d.Env {
			if !slices.Contains(got.env, kv) {
				t.Errorf("%s: %s missing from the fallback's env: %q", name, kv, got.env)
			}
		}
		if !slices.Contains(got.env, user.env[0]) {
			t.Errorf("%s: the user's own --env was dropped: %q", name, got.env)
		}
	}
}

// How each field of runFlags behaves when a run is re-targeted.
//
// The same bargain internal/fleet's gates_test.go makes, for the same reason: a
// new field is a new way for a fallback container to differ from the one the
// user asked for, and the decision is better made here than noticed later in a
// transcript. Adding a field to runFlags fails this test until it is classified.
const (
	// perAgent — belongs to the agent, and retarget must replace it.
	perAgent = "per-agent"
	// perAttempt — the routing loop sets it before calling retarget, which must
	// therefore leave it alone.
	perAttempt = "per-attempt"
	// carried — describes the run rather than the agent, and is identical for
	// whoever ends up running it.
	carried = "carried"
)

var routingFieldPolicy = map[string]string{
	"persistName": perAgent,
	"env":         perAgent,
	"envAllow":    perAgent,
	"mounts":      perAgent,

	"routedFrom":   perAttempt,
	"routeReason":  perAttempt,
	"routeID":      perAttempt,
	"routeAttempt": perAttempt,

	// The run's own shape. Where it runs, what it may reach, what it may spend —
	// none of which changes because a different agent is running it.
	"project": carried, "image": carried, "workdir": carried, "user": carried,
	"tty": carried, "noTTY": carried, "config": carried, "profile": carried,
	"network": carried, "engine": carried, "build": carried, "dryRun": carried,
	"noMetrics": carried, "memory": carried, "cpus": carried,
	"noHardening": carried, "allow": carried, "cache": carried,
	"secrets": carried, "worktree": carried, "publish": carried,
	"addHosts": carried, "hostGateway": carried, "git": carried,
	"runtime": carried, "share": carried, "shareName": carried,
	"paste": carried, "detach": carried,
	"noSnapshot": carried, "snapshotInterval": carried,

	// The chain itself: the same list, whichever of its agents is on.
	"fallback": carried,

	// Opt-outs read by the wrapper that owns them, before the loop ever runs.
	// noSync and noStatusline are claude's and are inert once claude.go has
	// decided what to mount; noPersistAuth is everyone's, and switching the
	// persisted HOME off applies to the fallback exactly as it did to the primary.
	"noPersistAuth": carried, "noStatusline": carried, "noSync": carried,
}

func TestEveryRunFlagIsClassifiedForRouting(t *testing.T) {
	rt := reflect.TypeOf(runFlags{})
	seen := map[string]bool{}
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		seen[name] = true
		if _, ok := routingFieldPolicy[name]; !ok {
			t.Errorf("runFlags.%s is not classified in routingFieldPolicy.\n"+
				"Decide what a fallback attempt should do with it: %q if it belongs to "+
				"the agent and retarget must replace it, %q if the routing loop sets it, "+
				"%q if it describes the run and travels unchanged.",
				name, perAgent, perAttempt, carried)
		}
	}
	for name := range routingFieldPolicy {
		if !seen[name] {
			t.Errorf("routingFieldPolicy names %s, which runFlags no longer has", name)
		}
	}
}

// And the classification is checked against what retarget actually does, so an
// entry cannot be a wish. Every field is filled with a distinctive value first:
// a zero-valued struct would let "unchanged" and "never set" look alike, which
// is precisely the bug being guarded against.
func TestRetargetChangesExactlyTheAgentsFields(t *testing.T) {
	var base runFlags
	fill(reflect.ValueOf(&base).Elem())
	user := userInputs{
		mounts:   []string{"/home/me/notes:/notes:ro"},
		env:      []string{"TERM=xterm-256color"},
		envAllow: []string{"MY_OWN_TOKEN"},
	}
	droid, ok := agents.Lookup("droid")
	if !ok {
		t.Fatal("droid is not in the descriptor table")
	}

	got := retarget(base, droid, user, &handoff.Export{Dir: t.TempDir()})

	before, after := reflect.ValueOf(&base).Elem(), reflect.ValueOf(&got).Elem()
	rt := reflect.TypeOf(runFlags{})
	for i := range rt.NumField() {
		name := rt.Field(i).Name
		same := reflect.DeepEqual(readable(before.Field(i)).Interface(), readable(after.Field(i)).Interface())
		switch routingFieldPolicy[name] {
		case perAgent:
			if same {
				t.Errorf("runFlags.%s is classified %q but retarget left it alone — "+
					"the fallback is running with the primary's %s", name, perAgent, name)
			}
		case perAttempt, carried:
			if !same {
				t.Errorf("runFlags.%s is classified %q but retarget changed it: %v -> %v",
					name, routingFieldPolicy[name],
					readable(before.Field(i)), readable(after.Field(i)))
			}
		}
	}
}

// fill gives every field a value distinguishable from both the zero value and
// from anything retarget would put there.
//
// Reflection over unexported fields needs `readable` — these are one package's
// private flags, and the alternative is a hand-written literal that a new field
// slips silently past, which is the failure this file is about.
func fill(v reflect.Value) {
	for i := range v.NumField() {
		f, name := readable(v.Field(i)), v.Type().Field(i).Name
		switch f.Interface().(type) {
		case string:
			f.SetString("primary-" + strings.ToLower(name))
		case bool:
			f.SetBool(true)
		case int:
			f.SetInt(7)
		case time.Duration:
			f.Set(reflect.ValueOf(3 * time.Minute))
		case []string:
			f.Set(reflect.ValueOf([]string{"primary-" + strings.ToLower(name)}))
		default:
			panic("fill: unhandled kind for runFlags." + name)
		}
	}
}

// readable re-derives an addressable field through its own address, which is
// what lets a test in the same package read and write unexported fields
// generically. Nothing outside a test should do this.
func readable(f reflect.Value) reflect.Value {
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}
