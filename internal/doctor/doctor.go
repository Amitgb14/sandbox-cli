// Package doctor asks whether a host can deliver what a profile promises.
//
// It exists as its own package because two callers need the same answer and
// must not each have their own: `sandbox-cli doctor` renders it to a terminal,
// and Studio's GET /v1/doctor serves it as JSON. A second copy of "is seccomp
// applied" would drift from this one, and the two would then disagree about the
// host on the same machine — which is exactly the failure mode this command was
// written to prevent, arriving through the back door.
//
// The split is deliberate: this package produces *facts*, and the verdict —
// whether a fact blocks — is computed separately, because the same host can
// pass dev and fail prod. That is the profiles' own asymmetry, not an
// inconsistency. A control the host cannot provide is a **warning** under dev,
// where someone is reading it and the alternative is a tool that refuses to
// work on an ordinary laptop; it is a **failure** under prod, where nobody is
// watching and a run that degraded quietly is the thing the profile exists to
// prevent.
package doctor

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// Status is how one check came out.
type Status int

const (
	StatusOK Status = iota
	// StatusWeak is a control the host cannot provide. Whether that is fatal is
	// the profile's decision, not the check's — which is why the check reports a
	// fact and Verdict is computed separately.
	StatusWeak
	// StatusUnknown is a question that could not be asked. Deliberately distinct
	// from StatusWeak: prod treats it as a failure, because it does not get to
	// assume the answer it would prefer, while dev stays quiet rather than
	// warning about something it could not determine.
	StatusUnknown
)

// String is the wire name for this status, and the one a JSON client switches
// on. "pass"/"warn"/"unknown" rather than the Go constant names, because the
// client's vocabulary is a published contract and these constants are not.
func (s Status) String() string {
	switch s {
	case StatusOK:
		return "pass"
	case StatusWeak:
		return "warn"
	default:
		return "unknown"
	}
}

// Check is one host property and what to do when it is not satisfied.
type Check struct {
	Name   string
	Status Status
	Detail string
	Remedy string
}

// Timeout bounds the whole preflight. A wedged daemon must not hang the one
// command you run *because* you are unsure about the host — and the checks
// classify a timeout as "could not be asked" rather than "not satisfied", so a
// slow machine does not read as a broken one.
const Timeout = 90 * time.Second

// Runtime is the slice of the docker backend these checks need. An interface so
// they can be exercised without a daemon.
type Runtime interface {
	Available(context.Context) error
	SeccompUnavailable(context.Context) (bool, bool)
	Runtimes(context.Context) ([]string, error)
	FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string)
	ResolvesNames(context.Context, string) (runtime.DNSProbe, string)
	ImagePresent(context.Context, string) (bool, bool)
}

// NewRuntime is a var so tests can substitute a host.
var NewRuntime = func(engine string) Runtime { return runtime.NewEngine(engine) }

// RunChecks asks the host every question this profile depends on.
func RunChecks(ctx context.Context, profile, engine string) []Check {
	d := NewRuntime(engine)
	if engine == "" {
		engine = "docker"
	}

	if err := d.Available(ctx); err != nil {
		// Nothing below can be answered without a daemon, and reporting six
		// unknowns would bury the one fact that matters.
		return []Check{{
			Name:   engine + " daemon",
			Status: StatusWeak,
			Detail: err.Error(),
			Remedy: "start " + engine + " and run this again",
		}}
	}
	out := []Check{{Name: engine + " daemon", Status: StatusOK, Detail: "reachable"}}
	out = append(out, checkBaseImage(ctx, d))
	out = append(out, checkSeccomp(ctx, d))
	out = append(out, checkFirewall(ctx, d))
	out = append(out, checkDNS(ctx, d))
	out = append(out, checkRuntimes(ctx, d, profile))
	return out
}

// checkBaseImage is the everyday question the other checks do not answer: not
// "is this host safe enough", but "is my first run going to take three minutes".
//
// A missing image is reported as **ok**, deliberately, and it is worth saying
// why rather than leaving it looking like a mistake. Nothing is wrong with a
// machine that has not built the image yet — the first run builds it, which is
// the design. What the reader needs is not a warning but a heads-up, and the
// runtime check below already establishes that an "ok" here may still have
// something to tell you.
func checkBaseImage(ctx context.Context, d Runtime) Check {
	c := Check{Name: "base image"}
	ref := image.Ref()
	present, known := d.ImagePresent(ctx, ref)
	switch {
	case !known:
		c.Status = StatusUnknown
		c.Detail = "the daemon could not be asked which images it has"
	case present:
		c.Status = StatusOK
		c.Detail = "built and ready (" + ref + ")"
	default:
		c.Status = StatusOK
		c.Detail = "not built yet — the first run builds it, which takes a few minutes"
		c.Remedy = "build it now with `sandbox-cli run --build -- true` if you would rather wait for it here"
	}
	return c
}

func checkSeccomp(ctx context.Context, d Runtime) Check {
	c := Check{Name: "seccomp"}
	unavailable, answered := d.SeccompUnavailable(ctx)
	switch {
	case !answered:
		c.Status = StatusUnknown
		c.Detail = "the daemon could not be asked"
	case unavailable:
		c.Status = StatusWeak
		c.Detail = "no syscall filter is applied; the container gets the full syscall table"
		c.Remedy = "on Docker Desktop: Settings > Docker Engine, remove \"seccomp-profile\": \"unconfined\""
	default:
		c.Status = StatusOK
		c.Detail = "the daemon applies a profile"
	}
	return c
}

// checkFirewall is the check most worth having on an unfamiliar host, and the
// one that changed meaning when the egress allowlist became the default: every
// run now takes the root-entrypoint path with NET_ADMIN, so a daemon that cannot
// grant it now affects everybody rather than only those who passed --allow.
func checkFirewall(ctx context.Context, d Runtime) Check {
	c := Check{Name: "egress firewall"}
	switch probe, reason := d.FirewallProgrammable(ctx, image.Ref()); probe {
	case runtime.FirewallOK:
		c.Status = StatusOK
		c.Detail = "a container here can program the nat, redirect, owner and conntrack rules the firewall needs"
	case runtime.FirewallUnknown:
		// Not the host's fault, and not an answer either.
		c.Status = StatusUnknown
		c.Detail = reason
		c.Remedy = "run any sandbox command once to build the image, then try again"
	default:
		c.Status = StatusWeak
		c.Detail = "a container here cannot program the firewall: " + reason
		c.Remedy = "rootless or userns-remapped daemons often cannot; use --network default, " +
			"or run on a daemon that can grant NET_ADMIN"
	}
	return c
}

// checkDNS asks whether a container on a sandbox-shaped network can resolve a
// name, which is a hard requirement the tool creates for itself.
//
// Under podman every sandbox gets its own network and a custom podman network
// resolves through aardvark-dns, which the default rootless network does not —
// so sandbox-cli depends on a resolver it caused to be used. Where that resolver
// is broken the symptom arrives four layers away, as an agent hanging at login
// with `getaddrinfo ETIMEOUT`, which took several rounds to trace to its cause.
//
// Tried rather than queried, like the firewall check beside it, and for the same
// reason: whether aardvark-dns answers on this host is not something to ask
// about, it is something to find out.
//
// The two failure states are kept apart because only one of them is this tool's
// doing. A host that cannot resolve anywhere has no DNS and would fail whatever
// sandbox-cli did; a host that resolves on the engine's default network but not
// on ours is the reported bug, and is the one with a remedy.
func checkDNS(ctx context.Context, d Runtime) Check {
	c := Check{Name: "container DNS"}
	switch probe, reason := d.ResolvesNames(ctx, image.Ref()); probe {
	case runtime.DNSOK:
		c.Status = StatusOK
		c.Detail = "a container on a sandbox network can resolve names"
	case runtime.DNSSandboxBroken:
		c.Status = StatusWeak
		c.Detail = "a container resolves names on this engine's default network but not on a " +
			"sandbox one: " + reason
		c.Remedy = "under podman this is usually the per-sandbox network's resolver: try " +
			"`podman network reload --all` (and `sandbox-cli clean` first if leaked networks block it)"
	case runtime.DNSNoResolver:
		c.Status = StatusWeak
		c.Detail = "no container on this host can resolve names: " + reason
		c.Remedy = "this is the host's DNS rather than the sandbox network; an agent will fail at login until it works"
	default:
		// Not the host's fault, and not an answer either.
		c.Status = StatusUnknown
		c.Detail = reason
		c.Remedy = "run any sandbox command once to build the image, then try again"
	}
	return c
}

// unvouchedRuntimes are the registered names that are neither an ordinary
// shared-kernel runtime nor one this tool will call a kernel of its own.
//
// It asks internal/runtime rather than keeping a third list, for the reason
// checkRuntimes already gives about the second: the same two questions are
// answered about a running container in the session listing, and the answers
// have to agree.
// It asks runtime.SharedKernelRuntime and not ContainerInfo.NotTheHostDefault,
// which is the same question with the opposite normalisation and was the first
// version's bug. NotTheHostDefault looks the name up raw on purpose, so on every
// containerd-backed daemon — where `docker info` reports runc as
// io.containerd.runc.v2 — it classified plain runc as unrecognised and this
// check told the reader that runc "may still be stronger". Nothing here may ever
// suggest a boundary a run did not get.
func unvouchedRuntimes(names []string) []string {
	var out []string
	for _, n := range names {
		if runtime.StrongerRuntime(n) || runtime.SharedKernelRuntime(n) {
			continue
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// checkRuntimes reports whether a stronger-isolation runtime is registered.
//
// This is a warning even under prod, and the reasoning is worth stating rather
// than quietly deciding: prod covers untrusted agents, for which a shared kernel
// is not a boundary, so gVisor or a microVM is the honest answer. But sandbox-cli
// does not yet *select* one — the prod profile deliberately leaves Runtime at
// the host default rather than guessing a name — and failing a check for
// something the tool does not yet do would be theatre. It reports the gap
// truthfully and says what it means.
func checkRuntimes(ctx context.Context, d Runtime, profile string) Check {
	c := Check{Name: "isolation runtime"}
	names, err := d.Runtimes(ctx)
	if err != nil {
		c.Status = StatusUnknown
		c.Detail = "the daemon could not be asked which runtimes are registered"
		return c
	}
	var strong []string
	for _, n := range names {
		// One list, in internal/runtime: the listing has to make the same
		// judgement about a running container that this makes about a registered
		// runtime, and two copies of "which runtimes are stronger" would drift the
		// way two copies of a security-relevant list always do.
		if runtime.StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	sort.Strings(strong)
	if len(strong) > 0 {
		c.Status = StatusOK
		c.Detail = "stronger isolation available: " + strings.Join(strong, ", ") +
			" (select with `runtime:` in your config)"
		return c
	}
	c.Status = StatusOK // reported, not failed — see the doc comment
	// "only the default" would be a false sentence about a host that has an
	// unvouched-for runtime registered — a bare `kata-runtime`, or an admin's
	// `sysbox-runc`. Those are real runtimes that this tool declines to make a
	// claim about, which is a different thing from their not being there, and
	// printing them as the default is how a reader concludes they are absent.
	//
	// Falls through to the prod remedy below rather than returning: the gap is the
	// same one either way — no runtime this tool will vouch for — and prod's advice
	// about it is just as due to a host that has an unrecognised one registered.
	if unvouched := unvouchedRuntimes(names); len(unvouched) > 0 {
		c.Detail = "no runtime this tool vouches for; registered: " + strings.Join(names, ", ")
		// Name the unvouched subset only when it *is* a subset. On a host whose
		// only registered runtime is the unrecognised one, repeating the identical
		// list inside its own parenthesis reads as a second fact rather than the
		// same one twice.
		if len(unvouched) < len(names) {
			c.Detail += " (" + strings.Join(unvouched, ", ") + " may still be stronger)"
		} else {
			c.Detail += " (it may still be stronger)"
		}
		c.Detail += "; sandbox-cli only vouches for names that say which hypervisor " +
			"they use, e.g. kata-fc"
	} else {
		c.Detail = "only the default runtime is registered: " + strings.Join(names, ", ")
	}
	if profile == config.ProfileProd {
		// Kept out of Detail: a newline inside a tabwriter cell ends the column
		// block, so a check added after this one would silently misalign.
		c.Remedy = "prod may carry untrusted agents, and a shared kernel is not a boundary for those — " +
			"install gVisor (runsc) or Kata and set `runtime:`"
	}
	return c
}

// Verdict turns a status into a mark and whether it blocks, which is the one
// place the dev/prod asymmetry is expressed.
func Verdict(s Status, strict bool) (mark string, fatal bool) {
	switch s {
	case StatusOK:
		return "ok  ", false
	case StatusUnknown:
		if strict {
			return "FAIL", true
		}
		return "?   ", false
	default:
		if strict {
			return "FAIL", true
		}
		return "warn", false
	}
}
