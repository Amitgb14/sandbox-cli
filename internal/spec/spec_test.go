package spec

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
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
	p.Network.Ceiling = api.NetworkAllowlist
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
	// With no policy file the default is open.
	if s.Network.Mode != api.NetworkOpen || len(s.Network.Allow) != 0 {
		t.Errorf("network default not applied: %+v", s.Network)
	}
	// An operator's allowlist default is applied as given, and copied, not
	// aliased: a later edit to one sandbox's policy must not reach the
	// server's default.
	p.Network.Default = api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"pypi.org", "github.com"}}
	s, err = Resolve(api.CreateSandboxRequest{}, p, "sbx_2")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Network.Allow, p.Network.Default.Allow) || s.Network.Mode != api.NetworkAllowlist {
		t.Errorf("an allowlist default not applied: %+v", s.Network)
	}
	s.Network.Allow[0] = "mutated.example"
	if p.Network.Default.Allow[0] == "mutated.example" {
		t.Error("the resolved allow list aliases the policy's default")
	}
}

// Open by default, fitted to what a backend can give: open where it can, the
// allowlist with the baseline where it can only filter, none where it can do
// neither. Never none by surprise on a backend that can filter, and never open
// on one that cannot.
func TestDefaultPolicyFitsTheBackend(t *testing.T) {
	for name, c := range map[string]struct {
		caps          map[string]bool
		mode, ceiling string
	}{
		"open and allowlist": {map[string]bool{api.CapEgressOpen: true, api.CapEgressAllowlist: true}, api.NetworkOpen, api.NetworkOpen},
		"allowlist only":     {map[string]bool{api.CapEgressAllowlist: true}, api.NetworkAllowlist, api.NetworkAllowlist},
		"open only":          {map[string]bool{api.CapEgressOpen: true}, api.NetworkOpen, api.NetworkOpen},
		"no networking":      {map[string]bool{}, api.NetworkNone, api.NetworkNone},
	} {
		p := DefaultPolicyFor(c.caps)
		if p.Network.Default.Mode != c.mode || p.Network.Ceiling != c.ceiling {
			t.Errorf("%s: default %s, ceiling %s; want %s, %s", name, p.Network.Default.Mode, p.Network.Ceiling, c.mode, c.ceiling)
		}
		if c.mode == api.NetworkAllowlist && !reflect.DeepEqual(p.Network.Default.Allow, policy.BaselineEgress()) {
			t.Errorf("%s: the fallback allowlist is %v, want the baseline", name, p.Network.Default.Allow)
		}
		if err := p.Validate(); err != nil {
			t.Errorf("%s: the fitted policy is invalid: %v", name, err)
		}
	}
}

// Under an open default, "an allowlist" without names is the built-in
// baseline — agents' APIs and registries — and never an empty list.
func TestAnUnnamedAllowlistUnderAnOpenDefaultIsTheBaseline(t *testing.T) {
	s, err := Resolve(api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist}}, DefaultPolicy(), "sbx_1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Network.Mode != api.NetworkAllowlist || !reflect.DeepEqual(s.Network.Allow, policy.BaselineEgress()) {
		t.Errorf("got %+v; want the allowlist with the baseline", s.Network)
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
	pol.Ceiling = api.NetworkAllowlist
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
	p.Network.Ceiling, p.Network.Default = api.NetworkAllowlist, api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: policy.BaselineEgress()}
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
	// allow_bind went with binds: a policy still turning it on is refused at
	// startup rather than leaving its operator believing binds are served.
	if _, err := LoadPolicy(write("allow_bind: true\n")); err == nil || !strings.Contains(err.Error(), "allow_bind") {
		t.Errorf("a policy setting allow_bind: %v", err)
	}
	// A default above the ceiling is incoherent.
	if _, err := LoadPolicy(write("network: {default: {mode: open}, ceiling: allowlist}\n")); err == nil {
		t.Error("a default above the ceiling was accepted")
	}
	// Pools: parsed, bounded, one per image, and only for permitted images.
	p, err = LoadPolicy(write("pools: [{size: 2}, {image: other:1, size: 1}]\n"))
	if err != nil || len(p.Pools) != 2 || p.Pools[0].Size != 2 || p.Pools[1].Image != "other:1" {
		t.Errorf("pools: %+v %v", p.Pools, err)
	}
	for _, bad := range []string{
		"pools: [{size: 0}]\n",
		"pools: [{size: 33}]\n",
		"pools: [{size: 1}, {size: 2}]\n",
		"images: [a:1]\ndefault_image: a:1\npools: [{image: b:1, size: 1}]\n",
	} {
		if _, err := LoadPolicy(write(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
