package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Reaping what a run left behind.
//
// The listing itself lives in session.go; this is the other half — removing the
// containers that were kept on purpose, and the networks nothing is on any more.
//
// Two things leave containers behind:
//
//   - A `kill -9` on sandbox-cli. The daemon owns the container, not the `docker
//     run` client, so killing the client leaves the agent running and still
//     writing to /workspace. --rm is daemon-side, so it is reaped when it
//     eventually exits — but nothing stops it, and nothing tells the user it is
//     there.
//   - `--detach`, which sets Remove=false on purpose: the exit code and the logs
//     are the whole supervision story and --rm would discard both at the moment
//     they become interesting. Reaping was always meant to be "a later, explicit
//     step"; this is that step.

func newCleanCmd() *cobra.Command {
	var force bool
	var engineFlag, cfgPath string
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove sandbox sessions that have finished",
		Long: "Removes the containers of sessions that have exited.\n\n" +
			"Detached and fleet runs are deliberately kept after they finish so their exit\n" +
			"code and their logs survive; this is how you reap them once you have read\n" +
			"what you needed. Running sessions are never touched without --force, because\n" +
			"one of them may be an agent still working in your project — and with --force\n" +
			"they are asked to exit first, exactly as `sandbox-cli kill` asks, rather than\n" +
			"being shot mid-write.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, engine, err := sessionEngine(cfgPath, engineFlag)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			infos, err := sandboxSessions(ctx, rt, engine, true)
			if err != nil {
				return err
			}
			var targets []runtime.ContainerInfo
			for _, c := range infos {
				// Anything not plainly finished counts as live: running, restarting,
				// paused and an unreadable state all mean do not touch this without
				// --force.
				if !sessionFinished(c) && !force {
					continue
				}
				targets = append(targets, c)
			}
			if len(targets) == 0 {
				fmt.Println("nothing to clean")
				// Still try the networks: "nothing to remove" is precisely the state in
				// which a leftover network is worth collecting, and returning early
				// meant it never was.
				removeSandboxNetworkIfUnused(ctx, rt, engine)
				return nil
			}
			for _, c := range targets {
				if !sessionFinished(c) {
					// --force reached a live agent. Stop is SIGTERM plus a grace period,
					// so it gets to finish the write it was in the middle of; `rm -f`
					// used to take that away, and the file it was writing is in the
					// user's project.
					if err := rt.Stop(ctx, c.ID); err != nil {
						fmt.Fprintf(os.Stderr, "sandbox-cli: %v\n", err)
						continue
					}
				}
				if err := rt.Remove(ctx, c.ID); err != nil {
					fmt.Fprintf(os.Stderr, "sandbox-cli: %v\n", err)
					continue
				}
				fmt.Printf("removed %s (%s)\n", termsafe.Clean(c.Name), sessionStatus(c))
			}
			removeSandboxNetworkIfUnused(ctx, rt, engine)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "also remove RUNNING sessions — one may be an agent still working")
	addSessionFlags(cmd, &engineFlag, &cfgPath)
	return cmd
}

// sessionFinished reports whether a session is over, which is the question
// `clean` asks. Deliberately narrower than "not running": a paused or restarting
// container is somebody's live run in an odd moment, and reaping it because it
// was not literally "running" at the instant we looked is how a supervision
// command destroys the thing it supervises. An unreadable state ("") counts as
// live for the same reason — not knowing is not a licence.
//
// `created` is the exception, and it is one of kind rather than degree: a
// container that never started ran no agent, wrote nothing, and has no in-flight
// write to protect. A detached run whose start failed leaves exactly that husk
// behind, with Remove=false, and without this case the only way to remove it was
// `clean --force` — whose flag help warns about killing an agent that may still
// be working, which is a much scarier thing than what the user is actually doing.
func sessionFinished(c runtime.ContainerInfo) bool {
	return c.State == "exited" || c.State == "dead" || c.State == "created"
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// engineBin is the container binary the supporting commands should speak to.
//
// ps, clean and stats used to hardcode docker, which made a detached podman run
// invisible to all three: `podman ps` showed it, `sandbox-cli ps` showed a
// docker container instead, and `clean` reported nothing to do while both sat
// there. A supervision command that cannot see the thing it supervises is worse
// than absent, because it answers.
func engineBin(cfgPath, engineFlag string) string {
	if engineFlag != "" {
		return engineFlag
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	cfg, err := config.LoadProfile(wd, cfgPath, "")
	if err != nil || cfg.Engine == "" {
		return "docker"
	}
	return cfg.Engine
}

// removeSandboxNetworkIfUnused reaps the shared network once nothing is on it.
//
// It is one long-lived object rather than one per run, so this is tidiness rather
// than the orphan problem a per-run network would have created — and it is
// deliberately conditional: removing a network another sandbox is still attached
// to would take that sandbox's networking away mid-run. docker refuses that
// anyway, but relying on the refusal would mean printing an error for a normal
// situation.
func removeSandboxNetworkIfUnused(ctx context.Context, rt sessionRuntime, bin string) {
	// The reaper runs first, unconditionally. It used to sit behind the
	// "no containers left" guard, which meant it never ran on a podman-only
	// machine (the listing failed against a missing docker) and was
	// short-circuited on a mixed one by any docker container. Since a detached
	// run deliberately leaves its network with machine lifetime, that was a leak
	// with no collector.
	reapPerRunNetworks(ctx, rt)

	infos, err := sandboxSessions(ctx, rt, bin, true)
	if err != nil || len(infos) > 0 {
		return
	}
	if err := exec.Command(bin, "network", "inspect", runtime.SandboxNetwork).Run(); err != nil {
		return // not there; nothing to do
	}
	if out, err := exec.Command(bin, "network", "rm", runtime.SandboxNetwork).CombinedOutput(); err != nil {
		// Something outside sandbox-cli may be attached. Not an error worth
		// failing the command over.
		_ = out
		return
	}
	fmt.Printf("removed network %s\n", runtime.SandboxNetwork)
}

// reapPerRunNetworks removes the per-run networks podman leaves behind, and says
// what it could not remove.
//
// The silence was the bug. A leaked network with a dead container in its IPAM
// makes `podman network reload --all` fail for *every* network on the host — the
// documented repair after firewalld drops netavark's rules — so `clean`
// reporting success while leaving one turns a five-minute fix into a long
// detour. Issue #77.
func reapPerRunNetworks(ctx context.Context, rt sessionRuntime) {
	reaper, ok := rt.(interface {
		ReapPerRunNetworks(context.Context, string) []runtime.NetworkReap
	})
	if !ok {
		return
	}
	for _, r := range reaper.ReapPerRunNetworks(ctx, sandbox.LabelCLI) {
		if r.Removed {
			fmt.Printf("removed network %s\n", termsafe.Clean(r.Name))
			continue
		}
		// Not an error: a network still carrying a live sandbox is the normal
		// state, and `clean` has no business failing over it. It is printed
		// because an uncollected one has a host-wide cost nobody would attribute
		// to sandbox-cli.
		fmt.Printf("kept network %s (%s)\n", termsafe.Clean(r.Name), termsafe.Clean(r.Reason))
	}
}
