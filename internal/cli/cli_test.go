package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
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

// --allow adds to the server's default allowlist: someone typing one more host
// does not mean "and none of the others".
func TestBuildNetworkAddsToTheDefault(t *testing.T) {
	caps := api.Capabilities{Network: api.NetworkCeiling{Default: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}}}
	p := buildNetwork(&runFlags{allow: []string{"example.com"}}, caps)
	if p == nil || p.Mode != api.NetworkAllowlist || !reflect.DeepEqual(p.Allow, []string{"github.com", "example.com"}) {
		t.Fatalf("got %+v", p)
	}
	if p := buildNetwork(&runFlags{}, caps); p != nil {
		t.Fatalf("no flags should mean the server default, got %+v", p)
	}
	if p := buildNetwork(&runFlags{network: "none"}, caps); p.Mode != api.NetworkNone || p.Allow != nil {
		t.Fatalf("none: %+v", p)
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
	if err := workspace.WritePrivate(dir, ".claude/.credentials.json", []byte("x")); err == nil {
		t.Fatal("wrote through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(victim, ".credentials.json")); err == nil {
		t.Fatal("the link's target was written")
	}
	if err := os.Symlink(filepath.Join(victim, "f"), filepath.Join(dir, "file.json")); err != nil {
		t.Fatal(err)
	}
	if err := workspace.WritePrivate(dir, "file.json", []byte("x")); err == nil {
		t.Fatal("wrote through a symlinked file")
	}
	if err := workspace.WritePrivate(dir, "../escape.json", []byte("x")); err == nil {
		t.Fatal("wrote outside the directory")
	}
	if err := workspace.WritePrivate(dir, "ok/auth.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "ok/auth.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestBuildEnv(t *testing.T) {
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
