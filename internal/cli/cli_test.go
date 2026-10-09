package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// A wrapper consumes only leading sandbox flags; the agent's own flags — even
// ones that share a name with nothing of ours — pass through untouched.
func TestSplitWrapperArgs(t *testing.T) {
	for _, tc := range []struct {
		in             []string
		sandbox, agent []string
	}{
		{[]string{"--network", "none", "--dangerously-skip-permissions"}, []string{"--network", "none"}, []string{"--dangerously-skip-permissions"}},
		{[]string{"--keep", "-p", "hi"}, []string{"--keep"}, []string{"-p", "hi"}},
		{[]string{"--allow=a.com", "--", "--network", "x"}, []string{"--allow=a.com"}, []string{"--network", "x"}},
		{[]string{"resume", "--network", "none"}, nil, []string{"resume", "--network", "none"}},
		{nil, nil, nil},
	} {
		s, a := splitWrapperArgs(tc.in)
		if !reflect.DeepEqual(s, tc.sandbox) || !reflect.DeepEqual(a, tc.agent) {
			t.Errorf("split(%q) = %q, %q; want %q, %q", tc.in, s, a, tc.sandbox, tc.agent)
		}
	}
}

// The network a run asks for is the config's and the flags' together, checked
// against the profile as one, and never quietly the server's default instead:
//   - --allow adds to the baseline, unless the config turned the baseline off;
//   - an allowlist that resolves to nothing refuses the run (prod's shape when
//     it names no hosts) rather than falling through to the server's default,
//     which has github.com in it;
//   - a --network flag cannot take a run out of what its profile requires.
func TestNetworkFollowsTheConfigAndTheProfile(t *testing.T) {
	caps := api.Capabilities{Network: api.NetworkCeiling{Default: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}}}
	cases := []struct {
		name, config string
		rf           runFlags
		agentHost    string
		want         *api.NetworkPolicy // nil: the server's default
		refused      bool
	}{
		{name: "nothing set", want: nil},
		// The hole an open server default would open: an explicit allowlist
		// sent as "nothing", and so served open.
		{name: "an explicit allowlist is sent as one", config: "network:\n  mode: allowlist\n",
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}},
		{name: "an agent's API joins an explicit allowlist", config: "network:\n  mode: allowlist\n", agentHost: "api.anthropic.com",
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com", "api.anthropic.com"}}},
		{name: "prod running an agent reaches its API and nothing else", config: "profile: prod\n", agentHost: "api.anthropic.com",
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"api.anthropic.com"}}},
		{name: "an agent under none stays none", rf: runFlags{network: "none"}, agentHost: "api.anthropic.com",
			want: &api.NetworkPolicy{Mode: api.NetworkNone}},
		{name: "--allow adds to the baseline", rf: runFlags{allow: []string{"example.com"}},
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com", "example.com"}}},
		{name: "--network none", rf: runFlags{network: "none"}, want: &api.NetworkPolicy{Mode: api.NetworkNone}},
		{name: "--network open under dev", rf: runFlags{network: "open"}, want: &api.NetworkPolicy{Mode: api.NetworkOpen}},
		{name: "--deny alone keeps the baseline", rf: runFlags{deny: []string{"github.com"}},
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}, Deny: []string{"github.com"}}},
		{name: "baseline off with --allow", config: "network:\n  baseline: false\n  allow: [a.example]\n", rf: runFlags{allow: []string{"b.example"}},
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"a.example", "b.example"}}},
		{name: "baseline off and nothing named", config: "network:\n  mode: allowlist\n  baseline: false\n", refused: true},
		{name: "prod naming no hosts", config: "profile: prod\n", refused: true},
		{name: "prod with --allow", config: "profile: prod\n", rf: runFlags{allow: []string{"api.example"}},
			want: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"api.example"}}},
		{name: "prod with --network open", config: "profile: prod\nnetwork:\n  allow: [api.example]\n", rf: runFlags{network: "open"}, refused: true},
		{name: "a mode nobody defines", rf: runFlags{network: "wide"}, refused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", home)
			if tc.config != "" {
				os.MkdirAll(filepath.Join(home, "sandbox"), 0o700)
				if err := os.WriteFile(filepath.Join(home, "sandbox", "config.yaml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			rf := tc.rf
			var req api.CreateSandboxRequest
			err := applyConfig(&rf, t.TempDir(), &req, caps, tc.agentHost)
			if tc.refused {
				if err == nil {
					t.Fatalf("not refused; network %+v", req.Network)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(req.Network, tc.want) {
				t.Fatalf("network %+v, want %+v", req.Network, tc.want)
			}
		})
	}
}

// With nothing asked for, the server's default applies; an agent run spells it
// out only when that default is an allowlist missing the agent's API.
func TestWithAgentAPI(t *testing.T) {
	claude, _ := agents.Lookup("claude")
	allowCaps := api.Capabilities{Network: api.NetworkCeiling{Default: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"pypi.org"}}}}
	openCaps := api.Capabilities{Network: api.NetworkCeiling{Default: api.NetworkPolicy{Mode: api.NetworkOpen}}}
	if got := withAgentAPI(nil, &claude, allowCaps); got == nil || !reflect.DeepEqual(got.Allow, []string{"pypi.org", "api.anthropic.com"}) {
		t.Errorf("an allowlist default without the agent's API: %+v", got)
	}
	if got := withAgentAPI(nil, &claude, openCaps); got != nil {
		t.Errorf("an open default was spelled out: %+v", got)
	}
	if got := withAgentAPI(nil, nil, allowCaps); got != nil {
		t.Errorf("a plain command was given an agent's API: %+v", got)
	}
	none := &api.NetworkPolicy{Mode: api.NetworkNone}
	if got := withAgentAPI(none, &claude, allowCaps); got != none {
		t.Errorf("none was changed: %+v", got)
	}
}

// Saved logins come from a guest: a symlink planted in the login directory
// must not redirect the write anywhere.
func TestWritePrivateRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	victim := t.TempDir()
	if err := os.Symlink(victim, filepath.Join(dir, ".claude")); err != nil {
		t.Fatal(err)
	}
	if err := agenthome.WritePrivate(dir, ".claude/.credentials.json", []byte("x")); err == nil {
		t.Fatal("wrote through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(victim, ".credentials.json")); err == nil {
		t.Fatal("the link's target was written")
	}
	if err := os.Symlink(filepath.Join(victim, "f"), filepath.Join(dir, "file.json")); err != nil {
		t.Fatal(err)
	}
	if err := agenthome.WritePrivate(dir, "file.json", []byte("x")); err == nil {
		t.Fatal("wrote through a symlinked file")
	}
	if err := agenthome.WritePrivate(dir, "../escape.json", []byte("x")); err == nil {
		t.Fatal("wrote outside the directory")
	}
	if err := agenthome.WritePrivate(dir, "ok/auth.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "ok/auth.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestBuildEnv(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "k")
	t.Setenv("FORWARD_ME", "v")
	d, _ := agents.LookupInteractive("claude")
	env, err := buildEnv([]string{"FORWARD_ME", "SET=1", "NOT_SET_ANYWHERE_X9"}, &d)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_API_KEY"] != "k" || env["FORWARD_ME"] != "v" || env["SET"] != "1" {
		t.Errorf("env %v", env)
	}
	if _, ok := env["NOT_SET_ANYWHERE_X9"]; ok {
		t.Error("an unset name was forwarded")
	}
	if _, err := buildEnv([]string{"LD_PRELOAD=/x"}, nil); err == nil {
		t.Error("a reserved name was accepted")
	}
}

// A saved key fills a variable the environment leaves unset, and only that:
// the environment wins, and a name the agent does not read is never saved.
func TestBuildEnvSavedKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	d, _ := agents.Lookup("claude")
	if len(d.EnvAllow) == 0 {
		t.Skip("claude reads no variables")
	}
	name := d.EnvAllow[0]
	t.Setenv(name, "")
	os.Unsetenv(name)
	if err := agenthome.SaveKey(d, name, "  saved-value\n"); err != nil {
		t.Fatal(err)
	}
	env, err := buildEnv(nil, &d)
	if err != nil {
		t.Fatal(err)
	}
	if env[name] != "saved-value" {
		t.Errorf("%s = %q, want the saved key, trimmed", name, env[name])
	}
	t.Setenv(name, "from-env")
	env, _ = buildEnv(nil, &d)
	if env[name] != "from-env" {
		t.Errorf("%s = %q, want the environment's value over the saved one", name, env[name])
	}
	if err := agenthome.SaveKey(d, "LD_PRELOAD", "/x"); err == nil {
		t.Error("saved a variable the agent does not read")
	}
	if err := agenthome.SaveKey(d, name, "a\x00b"); err == nil {
		t.Error("saved a key with a control character")
	}
	fi, err := os.Stat(filepath.Join(agenthome.ConfigDir(), "agent-keys.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("keys file: %v %v, want 0600", fi, err)
	}
	if err := agenthome.DeleteKey(d, name); err != nil {
		t.Fatal(err)
	}
	if len(agenthome.SavedKeys(d)) != 0 {
		t.Error("a deleted key is still saved")
	}
}

// A wrapper's help lists the sandbox flags it accepts. cobra answers --help
// before RunE, and a wrapper declares no flags of its own, so they were
// listed nowhere.
func TestWrapperHelpListsTheSandboxFlags(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"agent", "claude", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--fallback", "--network", "--no-persist-auth", "goes to claude", "sandbox-cli agent claude"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help does not mention %s:\n%s", want, out.String())
		}
	}
}

// The top level is the sandbox: every command there works for any command run
// in one. Agents are a layer on top, under `agent`, so none of their names —
// nor fleet — may appear beside run.
func TestTopLevelIsAgentNeutral(t *testing.T) {
	top := map[string]bool{}
	for _, c := range NewRootCmd().Commands() {
		top[c.Name()] = true
	}
	for _, name := range append(agents.InteractiveNames(), "fleet", "usage") {
		if top[name] {
			t.Errorf("%q is a top-level command; it belongs under `agent`", name)
		}
	}
	if !top["agent"] || !top["run"] {
		t.Errorf("top level: %v", top)
	}
}

// Every load is validated as a whole, as beta.15's was: a reserved name is an
// instruction to the guest's shell or loader, so no config sets it, the user's
// own included, by env: or as a secret's name; and a network mode nobody
// defines is an error, not the server's default.
func TestLoadConfigValidatesTheMergedConfig(t *testing.T) {
	for _, tc := range []struct{ user, project, says string }{
		{user: "env:\n  LD_PRELOAD: /tmp/x.so\n", says: "LD_PRELOAD"},
		{user: "secrets:\n  BASH_ENV:\n    env: HOME\n", says: "BASH_ENV"},
		{user: "network:\n  mode: allowlst\n", says: "network.mode"},
		{user: "secrets:\n  TOKEN:\n    env: HOME\n    file: /x\n", says: "exactly one"},
		{project: "env:\n  PS4: x\n", says: "may not"},
	} {
		home, project := t.TempDir(), t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", home)
		if tc.user != "" {
			os.MkdirAll(filepath.Join(home, "sandbox"), 0o700)
			os.WriteFile(filepath.Join(home, "sandbox", "config.yaml"), []byte(tc.user), 0o600)
		}
		if tc.project != "" {
			os.WriteFile(filepath.Join(project, ".sandbox.yaml"), []byte(tc.project), 0o644)
		}
		_, err := loadConfig(project, "", "", policy.Overrides{})
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("user %q project %q: %v, want an error naming %q", tc.user, tc.project, err, tc.says)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := loadConfig(t.TempDir(), "", "", policy.Overrides{}); err != nil {
		t.Errorf("no config at all: %v", err)
	}
}

// prod keeps no refresh token: the profile turns persisted logins off for
// every run that takes its configuration through applyConfig, fleet tasks
// included (their runner is given cfg.PersistAuthEnabled from the same load).
func TestProdTurnsPersistedLoginsOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	os.MkdirAll(filepath.Join(home, "sandbox"), 0o700)
	os.WriteFile(filepath.Join(home, "sandbox", "config.yaml"), []byte("profile: prod\nnetwork:\n  allow: [api.example]\n"), 0o600)
	rf := &runFlags{}
	if err := applyConfig(rf, t.TempDir(), &api.CreateSandboxRequest{}, api.Capabilities{}, ""); err != nil {
		t.Fatal(err)
	}
	if !rf.noPersistAuth {
		t.Error("a prod run restores and saves the agent's login")
	}
	cfg, err := loadConfig(t.TempDir(), "", "", policy.Overrides{})
	if err != nil || cfg.PersistAuthEnabled() {
		t.Errorf("prod's config allows persisted logins (%v)", err)
	}
}

// kill never infers its target: stopping the wrong sandbox costs its work.
func TestKillNeedsANamedTarget(t *testing.T) {
	if err := newKillCmd().Args(newKillCmd(), nil); err == nil {
		t.Error("kill with no sandbox named was accepted")
	}
}
