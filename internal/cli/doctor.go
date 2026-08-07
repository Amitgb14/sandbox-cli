package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/doctor"
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

// The check logic lives in internal/doctor so Studio's GET /v1/doctor asks the
// same questions of the same host. What stayed here is the rendering and the
// exit code — the terminal half, which an API has no use for.
type check = doctor.Check

const doctorTimeout = doctor.Timeout

var runDoctorChecks = doctor.RunChecks

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
			name, engine, runtimeName, err := resolveDoctorTarget(cfgPath, profile)
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
			return reportDoctor(name, runDoctorChecks(ctx, name, engine, runtimeName))
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
func resolveDoctorTarget(cfgPath, flag string) (profile, engine, runtimeName string, err error) {
	wd, werr := os.Getwd()
	if werr != nil {
		wd = "."
	}
	cfg, err := config.LoadProfile(wd, cfgPath, flag)
	if err != nil {
		// A configuration that cannot resolve is itself the finding, and the
		// message already says which key is at fault.
		return "", "", "", err
	}
	// The engine matters as much as the profile: checking docker on a machine
	// configured for podman answers a question nobody asked, and would have
	// reported a healthy docker while every run went elsewhere.
	// The selected runtime comes along for the same reason: under prod, "this
	// host can give a run its own kernel and nothing selected one" is a finding,
	// and it cannot be made without knowing what was selected.
	return cfg.Profile, cfg.Engine, cfg.Runtime, nil
}

// reportDoctor prints the findings and returns a non-zero-exit error when the
// profile cannot be satisfied.
func reportDoctor(profile string, checks []check) error {
	fmt.Printf("profile: %s\n\n", profile)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	strict := profile == config.ProfileProd
	var blocking []string
	weaker := 0

	for _, c := range checks {
		if c.Status != doctor.StatusOK {
			weaker++
		}
		mark, fatal := doctor.Verdict(c.Status, strict)
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", mark, c.Name, c.Detail)
		// Printed whatever the status: the runtime check is deliberately an "ok"
		// that still has something to tell you, and dropping its remedy silently
		// left the actionable half off the screen.
		if c.Remedy != "" {
			fmt.Fprintf(tw, "\t\t%s\n", c.Remedy)
		}
		if fatal {
			blocking = append(blocking, c.Name)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Println()

	if len(blocking) == 0 {
		// Two readings of the same checks. Under prod the question was "may this
		// host run unattended work", and the answer is a verdict. Under dev the
		// question someone actually typed is "is my setup good?", which a line
		// about profiles does not answer — so say plainly that it will work, and
		// how much of what prod would insist on is missing, without pretending
		// that makes it broken.
		switch {
		case strict:
			fmt.Printf("this host can run the %s profile\n", profile)
		case weaker == 0:
			fmt.Println("ready: everything sandbox-cli needs is here")
		default:
			fmt.Printf("ready for everyday use — %s above %s weaker than `--profile prod` would accept\n",
				plural(weaker, "check", "checks"), isAre(weaker))
		}
		return nil
	}
	// An error, so the exit code is non-zero and a scheduler notices.
	return fmt.Errorf("this host cannot satisfy the %s profile: %s\n"+
		"  fix the above, or use --profile dev, which warns instead of refusing",
		profile, strings.Join(blocking, ", "))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
