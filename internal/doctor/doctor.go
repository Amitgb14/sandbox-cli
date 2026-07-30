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
		switch n {
		case "runsc", "runsc-kvm", "kata", "kata-runtime", "kata-qemu", "crun-vm":
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
	c.Detail = "only the default runtime is registered: " + strings.Join(names, ", ")
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
