package spec

import (
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

func isRefused(err error) bool { var r *Refused; return errors.As(err, &r) }
func isInvalid(err error) bool { var i *Invalid; return errors.As(err, &i) }

func TestDefaultPolicyIsValid(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
}

// A policy whose default would be refused by its own ceiling is a server that
// gives more to a client who asks for nothing than to one who asks politely.
func TestValidateRefusesADefaultAboveTheCeiling(t *testing.T) {
	p := DefaultPolicy()
	p.Network.Default = api.NetworkPolicy{Mode: api.NetworkOpen}
	if err := p.Validate(); err == nil {
		t.Fatal("a default of open under an allowlist ceiling was accepted")
	}
	p = DefaultPolicy()
	p.Network.Default = api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{}}
	if err := p.Validate(); err == nil {
		t.Fatal("an empty default allowlist was accepted")
	}
}

func TestResolveAppliesDefaults(t *testing.T) {
	p := DefaultPolicy()
	s, err := Resolve(api.CreateSandboxRequest{}, p, "sbx_1")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "sbx_1" || s.Image != p.DefaultImage || s.CPUs != p.DefaultCPUs ||
		s.MemoryMB != p.DefaultMemoryMB || s.DiskMB != p.DefaultDiskMB {
		t.Errorf("defaults not applied: %+v", s)
	}
	if !reflect.DeepEqual(s.Network.Allow, p.Network.Default.Allow) || s.Network.Mode != api.NetworkAllowlist {
		t.Errorf("network default not applied: %+v", s.Network)
	}
	// The default is copied, not aliased: a later edit to one sandbox's policy
	// must not reach the server's default.
	s.Network.Allow[0] = "mutated.example"
	if p.Network.Default.Allow[0] == "mutated.example" {
		t.Error("the resolved allow list aliases the policy's default")
	}
}

func TestResolveRefusesLimitsAndBadValues(t *testing.T) {
	p := DefaultPolicy()
	for name, req := range map[string]api.CreateSandboxRequest{
		"too many cpus":    {CPUs: p.Limits.MaxCPUs + 1},
		"negative memory":  {MemoryMB: -1},
		"disk over limit":  {DiskMB: p.Limits.MaxDiskMB + 1},
		"image as a flag":  {Image: "--privileged"},
		"image with space": {Image: "a b"},
		"bad env name":     {Env: map[string]string{"1X": "v"}},
		"NUL in env value": {Env: map[string]string{"X": "a\x00b"}},
		"underscore name":  {Name: "sbx_abc"},
	} {
		if _, err := Resolve(req, p, "sbx_1"); !isInvalid(err) {
			t.Errorf("%s: err = %v; want invalid", name, err)
		}
	}
}

func TestResolveRefusesReservedEnvAndUnlistedImages(t *testing.T) {
	p := DefaultPolicy()
	for _, k := range []string{"LD_PRELOAD", "BASH_ENV", "SANDBOX_EGRESS_ALLOW"} {
		if _, err := Resolve(api.CreateSandboxRequest{Env: map[string]string{k: "x"}}, p, "sbx_1"); !isRefused(err) {
			t.Errorf("%s: err = %v; want refused", k, err)
		}
	}
	p.Images = []string{"approved:1"}
	if _, err := Resolve(api.CreateSandboxRequest{Image: "other:1"}, p, "sbx_1"); !isRefused(err) {
		t.Errorf("unlisted image: err = %v; want refused", err)
	}
	if _, err := Resolve(api.CreateSandboxRequest{Image: "approved:1"}, p, "sbx_1"); err != nil {
		t.Errorf("listed image refused: %v", err)
	}
}

func net(mode string, allow, deny []string) *api.NetworkPolicy {
	return &api.NetworkPolicy{Mode: mode, Allow: allow, Deny: deny}
}

// The whole rule, as a table: a request may tighten and never loosen, and a
// refusal is a refusal rather than a quiet clamp.
func TestNetworkTightenNeverLoosen(t *testing.T) {
	pol := NetworkPolicy{
		Default:  api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com", "pypi.org"}},
		Ceiling:  api.NetworkAllowlist,
		MayAllow: []string{"*.example.com", "registry.npmjs.org"},
	}
	cases := []struct {
		name string
		in   *api.NetworkPolicy
		want string // "ok", "refused", "invalid"
		mode string
		alw  []string
	}{
		{"omitted takes the default", nil, "ok", api.NetworkAllowlist, []string{"github.com", "pypi.org"}},
		{"none is always fine", net(api.NetworkNone, nil, nil), "ok", api.NetworkNone, nil},
		{"open is above the ceiling", net(api.NetworkOpen, nil, nil), "refused", "", nil},
		{"unknown mode", net("all", nil, nil), "invalid", "", nil},
		{"narrowing to a default name", net(api.NetworkAllowlist, []string{"github.com"}, nil), "ok", api.NetworkAllowlist, []string{"github.com"}},
		{"adding an exact may_allow name", net(api.NetworkAllowlist, []string{"registry.npmjs.org"}, nil), "ok", api.NetworkAllowlist, []string{"registry.npmjs.org"}},
		{"adding a name under a may_allow wildcard", net(api.NetworkAllowlist, []string{"api.example.com"}, nil), "ok", api.NetworkAllowlist, []string{"api.example.com"}},
		{"a narrower wildcard", net(api.NetworkAllowlist, []string{"*.a.example.com"}, nil), "ok", api.NetworkAllowlist, []string{"*.a.example.com"}},
		{"the apex is not under its wildcard", net(api.NetworkAllowlist, []string{"example.com"}, nil), "refused", "", nil},
		{"a broader wildcard", net(api.NetworkAllowlist, []string{"*.com"}, nil), "refused", "", nil},
		{"a lookalike suffix", net(api.NetworkAllowlist, []string{"evil-example.com"}, nil), "refused", "", nil},
		{"an unrelated name", net(api.NetworkAllowlist, []string{"attacker.net"}, nil), "refused", "", nil},
		{"empty allowlist", net(api.NetworkAllowlist, []string{}, nil), "refused", "", nil},
		{"allow with none", net(api.NetworkNone, []string{"github.com"}, nil), "invalid", "", nil},
		{"a URL is not a name", net(api.NetworkAllowlist, []string{"https://github.com"}, nil), "invalid", "", nil},
		{"a bare star is not a name", net(api.NetworkAllowlist, []string{"*"}, nil), "invalid", "", nil},
		{"case and trailing dot normalize", net(api.NetworkAllowlist, []string{"GitHub.com."}, nil), "ok", api.NetworkAllowlist, []string{"github.com"}},
		{"empty mode means the default mode", net("", []string{"pypi.org"}, nil), "ok", api.NetworkAllowlist, []string{"pypi.org"}},
	}
	for _, tc := range cases {
		got, err := resolveNetwork(tc.in, pol)
		switch tc.want {
		case "ok":
			if err != nil {
				t.Errorf("%s: %v", tc.name, err)
				continue
			}
			if got.Mode != tc.mode || !reflect.DeepEqual(got.Allow, tc.alw) {
				t.Errorf("%s: got %+v; want mode %q allow %v", tc.name, got, tc.mode, tc.alw)
			}
		case "refused":
			if !isRefused(err) {
				t.Errorf("%s: err = %v; want refused", tc.name, err)
			}
		case "invalid":
			if !isInvalid(err) {
				t.Errorf("%s: err = %v; want invalid", tc.name, err)
			}
		}
	}
}

// may_allow ["*"] permits any name — and still not a mode above the ceiling.
func TestMayAllowStarIsNotACeiling(t *testing.T) {
	pol := DefaultPolicy().Network
	if _, err := resolveNetwork(net(api.NetworkAllowlist, []string{"anything.dev"}, nil), pol); err != nil {
		t.Fatalf("may_allow [*] refused a name: %v", err)
	}
	if _, err := resolveNetwork(net(api.NetworkOpen, nil, nil), pol); !isRefused(err) {
		t.Fatalf("may_allow [*] let open through: %v", err)
	}
}

// Deny is a tightening, so it is always accepted — and under none it is dropped,
// since there is nothing for it to subtract from.
func TestDenyIsAlwaysAccepted(t *testing.T) {
	pol := DefaultPolicy().Network
	pol.MayAllow = nil
	got, err := resolveNetwork(net(api.NetworkAllowlist, nil, []string{"gist.github.com", "GIST.github.com"}), pol)
	if err != nil || !reflect.DeepEqual(got.Deny, []string{"gist.github.com"}) {
		t.Fatalf("deny: %+v, %v", got, err)
	}
	got, err = resolveNetwork(net(api.NetworkNone, nil, []string{"x.example.com"}), pol)
	if err != nil || got.Deny != nil {
		t.Fatalf("deny under none: %+v, %v", got, err)
	}
}

func TestUpdateUsesTheSameRule(t *testing.T) {
	p := DefaultPolicy()
	if _, err := ResolveNetworkUpdate(nil, p); !isInvalid(err) {
		t.Errorf("nil update: %v", err)
	}
	if _, err := ResolveNetworkUpdate(net(api.NetworkOpen, nil, nil), p); !isRefused(err) {
		t.Errorf("update to open: %v", err)
	}
}

func TestNewIDIsNotAName(t *testing.T) {
	id := NewID()
	if ValidName(id) {
		t.Fatalf("id %q is also a valid name, so a reference would be ambiguous", id)
	}
	if NewID() == id {
		t.Fatal("two ids collided")
	}
}

func TestLoadPolicy(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := dir + "/policy.yaml"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p, err := LoadPolicy(write(`
default_image: registry.example/base:1
images: [registry.example/base:1]
limits: {max_cpus: 4}
network:
  default: {mode: allowlist, allow: [GitHub.com., pypi.org]}
  ceiling: allowlist
  may_allow: ["*.corp.example"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultImage != "registry.example/base:1" || p.Limits.MaxCPUs != 4 || p.Limits.MaxMemoryMB != DefaultPolicy().Limits.MaxMemoryMB {
		t.Errorf("policy: %+v", p)
	}
	if !reflect.DeepEqual(p.Network.Default.Allow, []string{"github.com", "pypi.org"}) {
		t.Errorf("default allow not normalised: %v", p.Network.Default.Allow)
	}
	// A misspelt key is an error, not a silently ignored restriction.
	if _, err := LoadPolicy(write("network: {celing: none}\n")); err == nil {
		t.Error("an unknown key was accepted")
	}
	// A default above the ceiling is incoherent.
	if _, err := LoadPolicy(write("network: {default: {mode: open}, ceiling: allowlist}\n")); err == nil {
		t.Error("a default above the ceiling was accepted")
	}
}

// A bind is the one request field naming a host path, so it gets the
// non-overridable refusals, and only when the operator allowed binds at all.
func TestBindIsRefusedUnlessAllowedAndSafe(t *testing.T) {
	p := DefaultPolicy()
	dir := t.TempDir()
	if _, err := Resolve(api.CreateSandboxRequest{Bind: &api.Bind{HostPath: dir}}, p, "sbx_1"); !isRefused(err) {
		t.Errorf("bind with allow_bind off: %v", err)
	}
	p.AllowBind = true
	s, err := Resolve(api.CreateSandboxRequest{Bind: &api.Bind{HostPath: dir, ReadOnly: true}}, p, "sbx_1")
	if err != nil || s.Bind == nil || !s.Bind.ReadOnly {
		t.Fatalf("an ordinary directory: %+v, %v", s.Bind, err)
	}
	home, _ := os.UserHomeDir()
	for name, path := range map[string]string{"root": "/", "home": home, "relative": "rel/dir"} {
		if _, err := Resolve(api.CreateSandboxRequest{Bind: &api.Bind{HostPath: path}}, p, "sbx_1"); err == nil {
			t.Errorf("bind of %s (%s) was accepted", name, path)
		}
	}
	// FitTo turns it off where the backend cannot mount.
	if fitted, _ := p.FitTo(map[string]bool{}); fitted.AllowBind {
		t.Error("FitTo left bind on for a backend without bind_workspace")
	}
}
