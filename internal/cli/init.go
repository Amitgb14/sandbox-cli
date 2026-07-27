package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const scaffoldConfig = `# sandbox configuration (https://github.com/Amitgb14/sandbox-cli)
# Only /workspace (this project) is mounted into the container. HOME is fake and
# ephemeral. Uncomment and edit fields as needed.
#
# THIS FILE IS UNTRUSTED. It travels with the repository — anyone who clones the
# repo runs it, and the agent working in the sandbox can rewrite it between runs.
# So it may only describe the *project*, never the security boundary. These keys
# are refused here and belong in your own config (~/.config/sandbox/config.yaml)
# or on the command line:
#
#   image  workdir  user  home  runtime  mounts  secrets  env  env_allow
#   security.*  cache.paths  network.allow  ports  snapshot
#   and any network.mode / network.baseline that weakens what you already have
#
# If you have read a project file and want it anyway, load it deliberately:
#   sandbox-cli --config ./.sandbox.yaml claude

# Networking. A project may ask for STRICTER confinement than your own config
# (default -> allowlist -> none), never looser.
# In allowlist mode, outbound traffic is default-denied except DNS, established
# flows, a baseline of agent APIs + package registries, and the domains below —
# so npm/pip/git keep working while blocking arbitrary exfiltration. (Also
# available ad hoc via --allow DOMAIN.)
# Egress is already default-denied with the baseline, and a project file may only
# ever *tighten* what is in force — so "mode: default" here is a weakening, is
# refused, and would make every sandbox-cli command in this directory fail.
#
# To go stricter, uncomment BOTH lines (the parent and the child); a child on its
# own is not a "network" setting at all, and an unknown top-level key is dropped
# without complaint — so a request to tighten would be silently ignored.
# network:
#   mode: none            # reach nothing at all
#   allow:
#     - internal.registry.example.com
#   baseline: false     # drop the built-in domains so "allow" is the WHOLE list.
#                       # npm/pip/git then stop working unless you list their
#                       # hosts, and forgetting the agent's own API leaves it
#                       # unable to reach its model. Use when github.com must not
#                       # be reachable — it is a write endpoint, so a token in the
#                       # container can be pushed out through it. An empty list is
#                       # refused, not run open; "mode: none" reaches nothing.
#                       # (baseline: true is refused here — it widens egress.)

# Ports published to the host. A bare or HOST:CONTAINER spec binds 127.0.0.1;
# write 0.0.0.0:3000:3000 to expose it to your network deliberately.
# ports:
#   - 3000:3000

# Container hostname (cosmetic).
# hostname: sandbox

# Persist package-manager caches (npm/pip/cargo/go) in named volumes so they
# survive the disposable container. Opt-in; also available ad hoc via --cache.
# (cache.paths is user-config-only: it aims a writable volume at a container path.)
# cache:
#   enabled: true

# Crash safety net: periodic snapshots of the workspace under refs/sandbox.
# snapshot:
#   interval: 2m
#   retention: 336h

# ---------------------------------------------------------------------------
# For reference — these belong in ~/.config/sandbox/config.yaml, not here:
#
#   image: my-org/my-dev-image:latest
#   user: sandbox            # sandbox (non-root default) | root
#   runtime: runsc           # stronger boundary (gVisor / kata-runtime)
#   mounts:
#     - { host: ./data, container: /workspace/data, mode: rw }
#   env:
#     NODE_ENV: development
#   env_allow:               # host vars forwarded only if set
#     - ANTHROPIC_API_KEY
#     - OPENAI_API_KEY
#   security:
#     no_new_privileges: true
#     cap_drop: [ALL]
#     pids_limit: 1024
#   secrets:                 # resolved at run time, forwarded by name only
#     GITHUB_TOKEN:
#       command: gh auth token
`

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a .sandbox.yaml in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			wd, err := os.Getwd()
			if err != nil {
				return err
			}
			path := filepath.Join(wd, ".sandbox.yaml")
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.WriteFile(path, []byte(scaffoldConfig), 0o644); err != nil {
				return err
			}
			fmt.Printf("wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .sandbox.yaml")
	return cmd
}
