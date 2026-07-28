package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// `sandbox-cli doctor` answers one question: can this host deliver what the
// selected profile promises?
//
// It exists because prod runs unattended. Every other check in the codebase
// happens on the way into a run, which is the right place when someone is
// watching and a poor one when nobody is: an operator who discovers at 3am that
// the daemon cannot program iptables, or applies no syscall filter, learns it
// from a failed job rather than from the machine they were setting up.
//
// The verdict rule is the profiles' own asymmetry. A control that cannot be
// satisfied is a **warning** under dev, because a developer can act on it and
// the alternative is a tool that refuses to work on an ordinary laptop. It is a
// **failure** under prod, because nobody is watching and a production run that
// proceeded in a weaker configuration than it asked for is the thing the profile
// exists to prevent. So the same host can pass dev and fail prod, and that is
// the answer rather than an inconsistency.

// checkStatus is how one check came out.
type checkStatus int

const (
	statusOK checkStatus = iota
	// statusWeak is a control the host cannot provide. Whether that is fatal is
	// the profile's decision, not the check's — which is why the check reports a
	// fact and the verdict is computed separately.
	statusWeak
	// statusUnknown is a question that could not be asked. Deliberately distinct
	// from statusWeak: prod treats it as a failure, because it does not get to
	// assume the answer it would prefer, while dev stays quiet rather than
	// warning about something it could not determine.
	statusUnknown
)

// check is one host property and what to do when it is not satisfied.
type check struct {
	name   string
	status checkStatus
	detail string
	remedy string
}

// doctorTimeout bounds the whole preflight. A wedged daemon must not hang the
// one command you run *because* you are unsure about the host — and the checks
// classify a timeout as "could not be asked" rather than "not satisfied", so a
// slow machine does not read as a broken one.
const doctorTimeout = 90 * time.Second

func newDoctorCmd() *cobra.Command {
	var profile, cfgPath, engineFlag string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check whether this host can deliver what a profile promises",
		Long: "Checks the host, not a run: whether docker is reachable, whether it applies a\n" +
			"syscall filter, whether a container here can actually program the egress\n" +
			"firewall, and which OCI runtimes are registered.\n\n" +
			"The verdict follows the profile. Under dev a control the host cannot provide\n" +
			"is a warning, because you are here to read it. Under prod it is a failure and\n" +
			"the exit code is non-zero, because prod runs unattended and a run that\n" +
			"degraded quietly is worse than one that refused.\n\n" +
			"Run `sandbox-cli doctor --profile prod` on a machine before you schedule\n" +
			"anything on it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, engine, err := resolveDoctorTarget(cfgPath, profile)
			if err != nil {
				return err
			}
			if engineFlag != "" {
				// Validated rather than executed: an unvalidated flag turned
				// `--engine dokcer` into "dokcer not found on PATH" instead of the
				// config error Validate already gives for the same typo.
				if !runtime.KnownEngine(engineFlag) {
					return fmt.Errorf("--engine %q: want %s", engineFlag, strings.Join(runtime.EngineNames(), " or "))
				}
				engine = engineFlag
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), doctorTimeout)
			defer cancel()
			return reportDoctor(name, runDoctorChecks(ctx, name, engine))
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "profile to check against: dev (default) or prod")
	// The setups most worth preflighting are the checked-in ones loaded
	// deliberately, so the command that diagnoses a configuration has to be able
	// to read the same configuration a run would.
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "explicit config file path")
	cmd.Flags().StringVar(&engineFlag, "engine", "", "container engine to check: docker (default) or podman")
	return cmd
}

// resolveDoctorProfile honours the same layers a run would, so `doctor` reports
// on the profile that would actually be in force here rather than on a guess.
func resolveDoctorTarget(cfgPath, flag string) (profile, engine string, err error) {
	wd, werr := os.Getwd()
	if werr != nil {
		wd = "."
	}
	cfg, err := config.LoadProfile(wd, cfgPath, flag)
	if err != nil {
		// A configuration that cannot resolve is itself the finding, and the
		// message already says which key is at fault.
		return "", "", err
	}
	// The engine matters as much as the profile: checking docker on a machine
	// configured for podman answers a question nobody asked, and would have
	// reported a healthy docker while every run went elsewhere.
	return cfg.Profile, cfg.Engine, nil
}

// doctorRuntime is the slice of the docker backend doctor needs. An interface so
// the checks can be exercised without a daemon.
type doctorRuntime interface {
	Available(context.Context) error
	SeccompUnavailable(context.Context) (bool, bool)
	Runtimes(context.Context) ([]string, error)
	FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string)
}

// newDoctorRuntime is a var so tests can substitute a host.
var newDoctorRuntime = func(engine string) doctorRuntime { return runtime.NewEngine(engine) }

func runDoctorChecks(ctx context.Context, profile, engine string) []check {
	d := newDoctorRuntime(engine)
	if engine == "" {
		engine = "docker"
	}

	if err := d.Available(ctx); err != nil {
		// Nothing below can be answered without a daemon, and reporting six
		// unknowns would bury the one fact that matters.
		return []check{{
			name:   engine + " daemon",
			status: statusWeak,
			detail: err.Error(),
			remedy: "start " + engine + " and run this again",
		}}
	}
	out := []check{{name: engine + " daemon", status: statusOK, detail: "reachable"}}
	out = append(out, checkSeccomp(ctx, d))
	out = append(out, checkFirewall(ctx, d))
	out = append(out, checkRuntimes(ctx, d, profile))
	return out
}

func checkSeccomp(ctx context.Context, d doctorRuntime) check {
	c := check{name: "seccomp"}
	unavailable, answered := d.SeccompUnavailable(ctx)
	switch {
	case !answered:
		c.status = statusUnknown
		c.detail = "the daemon could not be asked"
	case unavailable:
		c.status = statusWeak
		c.detail = "no syscall filter is applied; the container gets the full syscall table"
		c.remedy = "on Docker Desktop: Settings > Docker Engine, remove \"seccomp-profile\": \"unconfined\""
	default:
		c.status = statusOK
		c.detail = "the daemon applies a profile"
	}
	return c
}

// checkFirewall is the check most worth having on an unfamiliar host, and the
// one that changed meaning when the egress allowlist became the default: every
// run now takes the root-entrypoint path with NET_ADMIN, so a daemon that cannot
// grant it now affects everybody rather than only those who passed --allow.
func checkFirewall(ctx context.Context, d doctorRuntime) check {
	c := check{name: "egress firewall"}
	switch probe, reason := d.FirewallProgrammable(ctx, image.Ref()); probe {
	case runtime.FirewallOK:
		c.status = statusOK
		c.detail = "a container here can program the nat, redirect, owner and conntrack rules the firewall needs"
	case runtime.FirewallUnknown:
		// Not the host's fault, and not an answer either.
		c.status = statusUnknown
		c.detail = reason
		c.remedy = "run any sandbox command once to build the image, then try again"
	default:
		c.status = statusWeak
		c.detail = "a container here cannot program the firewall: " + reason
		c.remedy = "rootless or userns-remapped daemons often cannot; use --network default, " +
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
func checkRuntimes(ctx context.Context, d doctorRuntime, profile string) check {
	c := check{name: "isolation runtime"}
	names, err := d.Runtimes(ctx)
	if err != nil {
		c.status = statusUnknown
		c.detail = "the daemon could not be asked which runtimes are registered"
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
		c.status = statusOK
		c.detail = "stronger isolation available: " + strings.Join(strong, ", ") +
			" (select with `runtime:` in your config)"
		return c
	}
	c.status = statusOK // reported, not failed — see the doc comment
	c.detail = "only the default runtime is registered: " + strings.Join(names, ", ")
	if profile == config.ProfileProd {
		// Kept out of detail: a newline inside a tabwriter cell ends the column
		// block, so a check added after this one would silently misalign.
		c.remedy = "prod may carry untrusted agents, and a shared kernel is not a boundary for those — " +
			"install gVisor (runsc) or Kata and set `runtime:`"
	}
	return c
}

// reportDoctor prints the findings and returns a non-zero-exit error when the
// profile cannot be satisfied.
func reportDoctor(profile string, checks []check) error {
	fmt.Printf("profile: %s\n\n", profile)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	strict := profile == config.ProfileProd
	var blocking []string

	for _, c := range checks {
		mark, fatal := verdict(c.status, strict)
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", mark, c.name, c.detail)
		// Printed whatever the status: the runtime check is deliberately an "ok"
		// that still has something to tell you, and dropping its remedy silently
		// left the actionable half off the screen.
		if c.remedy != "" {
			fmt.Fprintf(tw, "\t\t%s\n", c.remedy)
		}
		if fatal {
			blocking = append(blocking, c.name)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Println()

	if len(blocking) == 0 {
		fmt.Printf("this host can run the %s profile\n", profile)
		return nil
	}
	// An error, so the exit code is non-zero and a scheduler notices.
	return fmt.Errorf("this host cannot satisfy the %s profile: %s\n"+
		"  fix the above, or use --profile dev, which warns instead of refusing",
		profile, strings.Join(blocking, ", "))
}

// verdict turns a status into a mark and whether it blocks, which is the one
// place the dev/prod asymmetry is expressed.
func verdict(s checkStatus, strict bool) (mark string, fatal bool) {
	switch s {
	case statusOK:
		return "ok  ", false
	case statusUnknown:
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
