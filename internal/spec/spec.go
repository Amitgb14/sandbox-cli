// Package spec turns an API request into a fully resolved backend.Spec, against
// the server's policy, and refuses what the policy does not permit.
//
// Together with each backend's pure renderer this is where isolation is decided,
// and the only place. The server calls it and hands the result on; the backend
// renders it and never second-guesses it.
//
// The rule that shapes every function here is the one a project .sandbox.yaml
// was always held to, now applied to API requests: **a request may tighten what
// the server decided, never loosen it.** A request that would loosen is refused
// with the rule it broke — never clamped silently into something the caller did
// not ask for, because a caller that believes it got `open` and got `none`, or
// the reverse, makes decisions on a fact that is false.
package spec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/egressproxy"
	"github.com/Amitgb14/sandbox-cli/internal/hostpath"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// Refused is a well-formed request this server's policy will not grant. Retrying
// will not help; asking the operator might.
type Refused struct{ Msg string }

func (e *Refused) Error() string { return e.Msg }

// Invalid is a malformed request, or a value out of range.
type Invalid struct{ Msg string }

func (e *Invalid) Error() string { return e.Msg }

func refused(format string, a ...any) error { return &Refused{fmt.Sprintf(format, a...)} }
func invalid(format string, a ...any) error { return &Invalid{fmt.Sprintf(format, a...)} }

// Policy is what the server decided, which requests may narrow and never widen.
type Policy struct {
	DefaultImage string
	// Images, when non-empty, is the only images a request may name. Empty means
	// any — the local and self-hosted default, where whoever holds the token owns
	// the machine.
	Images []string

	DefaultCPUs     float64
	DefaultMemoryMB int
	DefaultDiskMB   int
	// DefaultIdleSecs applies when a request names no idle timeout; 0 means a
	// sandbox never idles out unless asked to.
	DefaultIdleSecs int
	Limits          api.Limits

	Network NetworkPolicy

	// AllowBind lets a request mount a host directory at /workspace. Off by
	// default, and only meaningful on a local endpoint, where whoever holds the
	// socket is the person whose directories they are.
	AllowBind bool
}

// NetworkPolicy is the server's egress floor and ceiling.
type NetworkPolicy struct {
	// Default applies to a request that sends no network policy.
	Default api.NetworkPolicy
	// Ceiling is the loosest mode a request may ask for.
	Ceiling string
	// MayAllow is the patterns a request's own allow entries must fall within.
	// ["*"] permits any name; empty permits only the names already in Default.
	MayAllow []string
}

// DefaultPolicy is the policy a server starts with when its operator configured
// nothing: the allowlist with the built-in baseline, which a request may extend
// with any name and may never turn into open egress. That is the dev profile's
// posture — open egress is a decision the operator makes, not one a request can.
func DefaultPolicy() Policy {
	return Policy{
		DefaultImage:    policy.DefaultImage,
		DefaultCPUs:     1,
		DefaultMemoryMB: 1024,
		DefaultDiskMB:   10240,
		DefaultIdleSecs: 1800,
		Limits:          api.Limits{MaxCPUs: 8, MaxMemoryMB: 16384, MaxDiskMB: 102400, MaxIdleTimeoutSecs: 7 * 24 * 3600},
		Network: NetworkPolicy{
			Default:  api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: policy.BaselineEgress()},
			Ceiling:  api.NetworkAllowlist,
			MayAllow: []string{"*"},
		},
	}
}

// Validate checks the policy is coherent, so a misconfigured server fails at
// startup rather than on its first request.
func (p Policy) Validate() error {
	if p.DefaultImage == "" {
		return fmt.Errorf("policy: default image is empty")
	}
	if api.NetworkRank(p.Network.Ceiling) < 0 {
		return fmt.Errorf("policy: network ceiling %q is not a mode", p.Network.Ceiling)
	}
	// The default must itself be a request the policy would grant, or a client
	// that sends nothing would get more than one that asked politely.
	ceilingOnly := p
	ceilingOnly.Network.MayAllow = nil
	if _, err := resolveNetwork(&p.Network.Default, ceilingOnly.Network); err != nil {
		return fmt.Errorf("policy: the default network policy is not permitted by the policy itself: %v", err)
	}
	if p.DefaultCPUs <= 0 || p.DefaultCPUs > p.Limits.MaxCPUs ||
		p.DefaultMemoryMB <= 0 || p.DefaultMemoryMB > p.Limits.MaxMemoryMB ||
		p.DefaultDiskMB <= 0 || p.DefaultDiskMB > p.Limits.MaxDiskMB {
		return fmt.Errorf("policy: default resources must be positive and within the limits")
	}
	return nil
}

// FitTo narrows the policy to what a backend can enforce, and says what it
// changed. A server whose backend cannot filter egress offers mode none and
// nothing else: advertising allowlist there would hand out sandboxes that are
// either open or offline while claiming to be filtered. The notes are for the
// operator; the narrowed policy is what clients see.
func (p Policy) FitTo(caps map[string]bool) (Policy, []string) {
	var notes []string
	n := p.Network
	if !caps[api.CapEgressAllowlist] && n.Ceiling == api.NetworkOpen && caps[api.CapEgressOpen] {
		// Open egress is all this backend can filter to; the operator asked for
		// it, so it stays, and only an allowlist default — unenforceable here —
		// falls back to none.
		if n.Default.Mode == api.NetworkAllowlist {
			notes = append(notes, "this backend cannot enforce an egress allowlist: the default is none, open is available on request")
			n.Default = api.NetworkPolicy{Mode: api.NetworkNone}
		}
		n.MayAllow = nil
	} else if !caps[api.CapEgressAllowlist] && api.NetworkRank(n.Ceiling) > api.NetworkRank(api.NetworkNone) {
		notes = append(notes, "this backend cannot enforce an egress allowlist: sandboxes get no network (mode none)")
		n.Ceiling = api.NetworkNone
		n.Default = api.NetworkPolicy{Mode: api.NetworkNone}
		n.MayAllow = nil
	} else if !caps[api.CapEgressOpen] && n.Ceiling == api.NetworkOpen {
		notes = append(notes, "this backend cannot offer open egress: the ceiling is allowlist")
		n.Ceiling = api.NetworkAllowlist
		if n.Default.Mode == api.NetworkOpen {
			n.Default = api.NetworkPolicy{Mode: api.NetworkNone}
		}
	}
	p.Network = n
	if p.AllowBind && !caps[api.CapBindWorkspace] {
		notes = append(notes, "this backend cannot mount host directories: bind is off")
		p.AllowBind = false
	}
	return p, notes
}

// Ceiling reports the policy the way /v1/capabilities shows it.
func (p Policy) Ceiling() api.NetworkCeiling {
	return api.NetworkCeiling{
		Default:  copyPolicy(p.Network.Default),
		Ceiling:  p.Network.Ceiling,
		MayAllow: append([]string{}, p.Network.MayAllow...),
	}
}

// nameRE is a sandbox name: lowercase, digits and dashes. No underscore, so a
// name can never collide with an id ("sbx_…"), and a reference is unambiguous.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// envNameRE is a portable environment variable name.
var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// imageRE is deliberately plain: registry/path:tag@digest characters only. A
// leading dash is excluded by the first class — an image reference that looks
// like a flag was an injection in the old tree's argv.
var imageRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)

// NewID returns a sandbox id.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return "sbx_" + hex.EncodeToString(b[:])
}

// ValidName reports whether s is a legal sandbox name.
func ValidName(s string) bool { return nameRE.MatchString(s) }

// Resolve turns a create request into a backend.Spec with the given id.
func Resolve(req api.CreateSandboxRequest, pol Policy, id string) (backend.Spec, error) {
	if req.Name != "" && !ValidName(req.Name) {
		return backend.Spec{}, invalid("name %q: lowercase letters, digits and dashes, starting with a letter or digit, at most 63", req.Name)
	}

	img := req.Image
	if img == "" {
		img = pol.DefaultImage
	}
	if !imageRE.MatchString(img) {
		return backend.Spec{}, invalid("image %q is not an image reference", img)
	}
	if len(pol.Images) > 0 && !contains(pol.Images, img) {
		return backend.Spec{}, refused("image %q is not one this server permits", img)
	}

	s := backend.Spec{ID: id, Image: img, CPUs: req.CPUs, MemoryMB: req.MemoryMB, DiskMB: req.DiskMB}
	if s.CPUs == 0 {
		s.CPUs = pol.DefaultCPUs
	}
	if s.MemoryMB == 0 {
		s.MemoryMB = pol.DefaultMemoryMB
	}
	if s.DiskMB == 0 {
		s.DiskMB = pol.DefaultDiskMB
	}
	switch {
	case s.CPUs < 0 || s.CPUs > pol.Limits.MaxCPUs:
		return backend.Spec{}, invalid("cpus %v: must be between 0 and %v", s.CPUs, pol.Limits.MaxCPUs)
	case s.MemoryMB < 0 || s.MemoryMB > pol.Limits.MaxMemoryMB:
		return backend.Spec{}, invalid("memory_mb %d: must be between 0 and %d", s.MemoryMB, pol.Limits.MaxMemoryMB)
	case s.DiskMB < 0 || s.DiskMB > pol.Limits.MaxDiskMB:
		return backend.Spec{}, invalid("disk_mb %d: must be between 0 and %d", s.DiskMB, pol.Limits.MaxDiskMB)
	}

	switch idle := req.IdleTimeoutSecs; {
	case idle < 0:
		return backend.Spec{}, invalid("idle_timeout_secs must not be negative")
	case idle == 0:
		s.IdleTimeoutSecs = pol.DefaultIdleSecs
	case pol.Limits.MaxIdleTimeoutSecs > 0 && idle > pol.Limits.MaxIdleTimeoutSecs:
		return backend.Spec{}, invalid("idle_timeout_secs %d: at most %d", idle, pol.Limits.MaxIdleTimeoutSecs)
	default:
		s.IdleTimeoutSecs = idle
	}

	env, err := ResolveEnv(req.Env)
	if err != nil {
		return backend.Spec{}, err
	}
	s.Env = env

	net, err := resolveNetwork(req.Network, pol.Network)
	if err != nil {
		return backend.Spec{}, err
	}
	s.Network = net

	if req.Bind != nil {
		if !pol.AllowBind {
			return backend.Spec{}, refused("this server does not mount host directories")
		}
		if !filepath.IsAbs(req.Bind.HostPath) {
			return backend.Spec{}, invalid("bind.host_path must be absolute")
		}
		// The non-overridable refusals: never /, never home, never an ancestor
		// of it — compared by identity, after symlinks are resolved.
		real, err := hostpath.ResolveWorkspace(req.Bind.HostPath)
		if err != nil {
			return backend.Spec{}, refused("bind: %v", err)
		}
		s.Bind = &backend.Bind{HostPath: real, ReadOnly: req.Bind.ReadOnly}
	}
	return s, nil
}

// ResolveEnv validates environment variables for a sandbox or a process.
//
// A reserved name is refused rather than dropped: those variables are
// instructions to the sandbox's own startup, the dynamic loader or the shell
// (policy.IsReservedEnv), and a caller who sent one either expected it to take
// effect or is probing whether it does. Either way the answer should be "no",
// out loud.
func ResolveEnv(in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if !envNameRE.MatchString(k) {
			return nil, invalid("environment variable name %q is not valid", k)
		}
		if policy.IsReservedEnv(k) {
			return nil, refused("environment variable %s is reserved: %s", k, policy.ReservedEnvReason())
		}
		if strings.ContainsRune(v, 0) {
			return nil, invalid("environment variable %s contains a NUL byte", k)
		}
		out[k] = v
	}
	return out, nil
}

// ResolveNetworkUpdate validates a policy change on a running sandbox. It is the
// same rule as create: an update is a request like any other, and the ceiling
// does not move because the sandbox already exists.
func ResolveNetworkUpdate(p *api.NetworkPolicy, pol Policy) (api.NetworkPolicy, error) {
	if p == nil {
		return api.NetworkPolicy{}, invalid("network: required")
	}
	return resolveNetwork(p, pol.Network)
}

func resolveNetwork(p *api.NetworkPolicy, pol NetworkPolicy) (api.NetworkPolicy, error) {
	if p == nil {
		return copyPolicy(pol.Default), nil
	}
	mode := p.Mode
	if mode == "" {
		mode = pol.Default.Mode
	}
	rank := api.NetworkRank(mode)
	if rank < 0 {
		return api.NetworkPolicy{}, invalid("network mode %q: want none, allowlist or open", p.Mode)
	}
	if rank > api.NetworkRank(pol.Ceiling) {
		return api.NetworkPolicy{}, refused("network mode %q is above this server's ceiling (%s)", mode, pol.Ceiling)
	}

	deny, err := normalizeNames("deny", p.Deny)
	if err != nil {
		return api.NetworkPolicy{}, err
	}
	allow, err := normalizeNames("allow", p.Allow)
	if err != nil {
		return api.NetworkPolicy{}, err
	}

	out := api.NetworkPolicy{Mode: mode, Deny: deny}
	switch mode {
	case api.NetworkNone, api.NetworkOpen:
		if len(allow) > 0 {
			return api.NetworkPolicy{}, invalid("network mode %q takes no allow list", mode)
		}
		if mode == api.NetworkNone {
			out.Deny = nil // nothing is reachable; a deny list has nothing to subtract from
		}
		return out, nil
	}

	// allowlist. An omitted allow list means the server's default one; a given
	// list is the whole list, so narrowing is just sending fewer names. Names
	// from the default are always permitted, since the server already chose them.
	if p.Allow == nil {
		allow = append([]string(nil), pol.Default.Allow...)
	} else {
		for _, name := range allow {
			if !contains(pol.Default.Allow, name) && !permitted(name, pol.MayAllow) {
				return api.NetworkPolicy{}, refused("allow %q: this server does not let a request add that name (may_allow: %v)", name, pol.MayAllow)
			}
		}
	}
	// An allowlist that resolved to nothing would be the strictest request
	// producing a sandbox with no filtering configured at all. `none` is how to
	// ask to reach nothing.
	if len(allow) == 0 {
		return api.NetworkPolicy{}, refused("network mode allowlist with an empty allow list; use mode none to reach nothing")
	}
	out.Allow = allow
	return out, nil
}

// permitted reports whether a requested allow entry falls within the server's
// may_allow patterns. A wildcard entry is permitted only by "*" or by a wildcard
// at least as broad: asking for *.a.example.com under *.example.com is
// narrowing; asking for *.example.com under a.example.com is not.
func permitted(name string, mayAllow []string) bool {
	for _, m := range mayAllow {
		if m == "*" {
			return true
		}
	}
	if rest, ok := strings.CutPrefix(name, "*."); ok {
		for _, m := range mayAllow {
			if mr, ok := strings.CutPrefix(m, "*."); ok && (rest == mr || strings.HasSuffix(rest, "."+mr)) {
				return true
			}
		}
		return false
	}
	return egressproxy.NewMatcher(mayAllow).Allows(name)
}

// hostRE is a DNS name, optionally behind a single leading "*." wildcard.
var hostRE = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// normalizeNames lowercases, strips a trailing dot, validates, dedupes and sorts.
// Names only: no scheme, port, path or address. Matching is by name, and a value
// that is not a name would either never match or match something other than
// what the caller meant.
func normalizeNames(field string, in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range in {
		n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(raw)), ".")
		if n == "*" || !hostRE.MatchString(n) || len(n) > 253 {
			return nil, invalid("%s %q: want a hostname such as example.com or *.example.com", field, raw)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

func copyPolicy(p api.NetworkPolicy) api.NetworkPolicy {
	return api.NetworkPolicy{
		Mode:  p.Mode,
		Allow: append([]string(nil), p.Allow...),
		Deny:  append([]string(nil), p.Deny...),
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
