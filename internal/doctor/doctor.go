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
	FirewallProgrammable(ctx context.Context, image, runtimeName string) (runtime.FirewallProbe, string)
	ImagePresent(context.Context, string) (bool, bool)
}

// NewRuntime is a var so tests can substitute a host.
var NewRuntime = func(engine string) Runtime { return runtime.NewEngine(engine) }

// RunChecks asks the host every question this profile depends on.
func RunChecks(ctx context.Context, profile, engine, selectedRuntime string) []Check {
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
	out = append(out, checkFirewall(ctx, d, selectedRuntime))
	out = append(out, checkRuntimes(ctx, d, profile, selectedRuntime))
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
//
// Probed under the runtime the run will select, because the answer depends on
// the kernel the container gets rather than on the daemon alone. A host running
// gVisor reported a healthy firewall from a runc probe while a runsc run could
// not program a single rule — the preflight passing and the launch failing,
// which is the disagreement ClassifyRuntimeGap was centralised to prevent.
func checkFirewall(ctx context.Context, d Runtime, selectedRuntime string) Check {
	c := Check{Name: "egress firewall"}
	switch probe, reason := d.FirewallProgrammable(ctx, image.Ref(), selectedRuntime); probe {
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
// Under prod this has teeth, and they close on the one thing that can be proved:
// an engine that reports a runtime with its own kernel while nothing selected
// it. Where there is no such evidence the check says so and does not fail —
// see runtime.ClassifyRuntimeGap for why an engine's silence is not proof that a
// stronger runtime was unavailable.
//
// The verdict itself is shared with the run path (sandbox.enforceKernelBoundary)
// so the preflight and the launch cannot reach different conclusions from the
// same evidence. What is local here is only how a verdict is *worded* and
// whether it is fatal, which is the profile's business rather than the engine's.
//
// selected is the runtime the run would actually use, not merely the configured
// one: `doctor --runtime X` preflights the command you are about to type, since
// --runtime on the run would otherwise change the answer after the check passed.
func checkRuntimes(ctx context.Context, d Runtime, profile, selected string) Check {
	c := Check{Name: "isolation runtime"}
	support := runtimeSupport(ctx, d)
	prod := profile == config.ProfileProd
	effective := support.EffectiveRuntime(selected)
	reported := strings.Join(support.All, ", ")

	switch runtime.ClassifyRuntimeGap(selected, support) {
	case runtime.GapNone:
		c.Status = StatusOK
		c.Detail = "runs get a kernel of their own: " + effective
		if selected == "" {
			c.Detail += " (this engine's default)"
		}
	case runtime.GapUnrecognised:
		// Not failed — the operator chose it — and not vouched for either.
		c.Status = StatusOK
		c.Detail = "runs on " + effective + ", which is not a runtime this tool recognises as having a kernel of its own"
	case runtime.GapMissing:
		// The launch would fail on this, so it is a finding on every profile:
		// a configuration that cannot work is not a matter of degree.
		c.Status = StatusWeak
		c.Detail = "runtime is set to " + effective + ", which this engine has not registered"
		c.Remedy = "register it, or name one of the runtimes with a kernel of their own that this engine has" +
			registeredSuffix(support)
	case runtime.GapNotSelected:
		c.Detail = "stronger isolation available: " + strings.Join(support.Registered, ", ") +
			" (select with `runtime:` in your config)"
		if prod {
			c.Status = StatusWeak
			c.Detail = "this engine can give a run its own kernel and neither the run nor its default uses one: " +
				strings.Join(support.Registered, ", ")
			// Only the stronger names: suggesting runc here would send an
			// operator to a second refusal.
			c.Remedy = "set `runtime: " + support.Registered[0] + "` in your own config — prod refuses a shared kernel it did not have to accept"
		}
	case runtime.GapUnknown:
		// Same verdict the run path reaches, so a preflight that passes is a run
		// that starts and one that fails is a run that would have been refused.
		c.Status = StatusUnknown
		c.Detail = "the engine could not be asked which runtimes it has"
	default: // GapUnverified
		c.Status = StatusOK
		c.Detail = "runs share the host kernel: this engine reported no runtime with one of its own"
		if reported != "" {
			// The names matter: gVisor installs itself as runsc-hostnet on some
			// setups, and an operator told only "install gVisor" would be
			// installing what they already have instead of naming what they have.
			c.Detail += " (it reported " + reported + ")"
		}
		if prod {
			c.Remedy = "a container namespace is not a boundary against a kernel exploit — " +
				"install gVisor (runsc) or Kata where you can, and name it with `runtime:`"
		}
	}
	return c
}

func registeredSuffix(s runtime.RuntimeSupport) string {
	if len(s.Registered) > 0 {
		return ": " + strings.Join(s.Registered, ", ")
	}
	if len(s.All) > 0 {
		return " (this engine reported only " + strings.Join(s.All, ", ") + ")"
	}
	return ""
}

// runtimeSupport asks the engine, once.
//
// The fallback is for a backend predating the interface upgrade, and it reports
// **Known: false** rather than assembling a verdict from whatever it can see:
// an engine that cannot be asked is unverified, and a fallback that quietly
// produced a passing verdict would be the permissive reading of an absence.
func runtimeSupport(ctx context.Context, d Runtime) runtime.RuntimeSupport {
	if r, ok := d.(interface {
		StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport
	}); ok {
		return r.StrongerRuntimeSupport(ctx)
	}
	names, err := d.Runtimes(ctx)
	if err != nil || len(names) == 0 {
		return runtime.RuntimeSupport{}
	}
	var strong []string
	for _, n := range names {
		if runtime.StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	sort.Strings(strong)
	// No default and no completeness claim: this backend cannot say which
	// runtime it would pick, and guessing "the first one" is how a listing
	// becomes an assertion.
	return runtime.RuntimeSupport{All: names, Registered: strong, Known: true}
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
