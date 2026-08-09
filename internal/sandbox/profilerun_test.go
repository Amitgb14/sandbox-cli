package sandbox

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// prodConfig is a configuration that satisfies prod at load time, so each test
// below isolates the *flag* as the only thing that could break it.
func prodConfig(t *testing.T) config.Config {
	t.Helper()
	// The real prod base, not a hand-built imitation: an imitation would drift
	// from the profile and these tests would stop describing it. XDG pinned so
	// the machine running them cannot contribute a layer.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := config.LoadProfile(t.TempDir(), "", config.ProfileProd)
	if err != nil {
		t.Fatalf("the prod base must load: %v", err)
	}
	// prod deliberately names no domains, and BuildSpec refuses an allowlist that
	// resolves to nothing — a refusal that would fire before the gate under test.
	cfg.Network.Allow = []string{"example.com"}
	if err := config.ValidateProfile(config.ProfileProd, cfg); err != nil {
		t.Fatalf("the base for these tests must itself satisfy prod: %v", err)
	}
	return cfg
}

// TestProdRefusesWhatAFlagWouldWiden is the regression for a hole the config
// check could not see: prod's guarantees are asserted against cfg, while
// --publish, --user, --memory, --cpus and --no-hardening arrive in Options and
// BuildSpec applies them over cfg afterwards.
//
// `run --profile prod --publish 0.0.0.0:8022:22` succeeded, published the
// container on every interface, and had the entrypoint open a matching hole in
// the default-deny INPUT chain — which is what prod's own message says cannot
// happen.
func TestProdRefusesWhatAFlagWouldWiden(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"publish", Options{Publish: []string{"0.0.0.0:8022:22"}}, "--publish"},
		{"root by name", Options{User: "root"}, "--user"},
		{"root by uid", Options{User: "0:0"}, "--user"},
		{"uncapped memory", Options{Memory: "0"}, "--memory"},
		{"uncapped cpus", Options{CPUs: "0"}, "--cpus"},
		{"hardening off", Options{NoHardening: true}, "--no-hardening"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Project = t.TempDir()
			tc.opts.Command = []string{"true"}

			_, err := BuildSpec(prodConfig(t), tc.opts)
			if err == nil {
				t.Fatalf("prod accepted %s: a flag must not widen what the profile guarantees", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal does not name %s: %v", tc.want, err)
			}
		})
	}
}

// TestProdAcceptsFlagsThatDoNotWiden: the gate must not refuse a run that only
// tightens or says nothing. A check that fired on an empty flag would make prod
// unusable, which is how a guarantee gets turned off wholesale.
func TestProdAcceptsFlagsThatDoNotWiden(t *testing.T) {
	for _, opts := range []Options{
		{},
		{Memory: "2g", CPUs: "1.5"},
		{User: "sandbox"},
	} {
		opts.Project = t.TempDir()
		opts.Command = []string{"true"}
		if _, err := BuildSpec(prodConfig(t), opts); err != nil {
			t.Errorf("prod refused %+v, which widens nothing: %v", opts, err)
		}
	}
}

// TestDevIsUntouched: the gate is prod's, and dev is the profile where a
// developer is watching and may debug with whatever they like.
func TestDevIsUntouched(t *testing.T) {
	cfg := config.Default()
	cfg.Profile = config.ProfileDev
	cfg.Network = config.NetworkSpec{Mode: "default"}
	opts := Options{
		Project: t.TempDir(), Command: []string{"true"},
		Publish: []string{"8080:80"}, NoHardening: true, Memory: "0",
	}
	if _, err := BuildSpec(cfg, opts); err != nil {
		t.Errorf("dev must not be subject to prod's refusals: %v", err)
	}
}
