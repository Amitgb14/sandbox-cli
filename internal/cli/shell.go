package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Connecting to a sandbox that is already running, the way a hosted sandbox
// service lets you open a terminal in one: `shell` for an interactive shell,
// `exec` for one command. Both start a new process in the sandbox and attach
// to it; `run` makes a new sandbox, and `attach` joins a process that is
// already there. Neither changes the sandbox's network, mounts or anything
// else about it: a process can only do what the sandbox already allows.

// shellArgv is the guest's best interactive shell: bash when the image has
// it, sh otherwise, as a login shell so the image's profile applies.
var shellArgv = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}

// connect starts argv in a running sandbox and attaches this terminal to it
// until it exits. The process is yours, so Ctrl-C without a terminal is
// forwarded to it; the sandbox carries on either way.
func connect(ctx context.Context, ctxFlag, ref string, argv []string, cwd string) error {
	c, _, err := newClient(ctxFlag)
	if err != nil {
		return err
	}
	return connectWith(ctx, c, ref, argv, cwd)
}

func connectWith(ctx context.Context, c *api.Client, ref string, argv []string, cwd string) error {
	sb, err := c.Sandbox(ctx, ref)
	if err != nil {
		return err
	}
	if sb.State != api.StateRunning {
		hint := ""
		if sb.State == api.StateSuspended {
			hint = " (resume it first: it keeps its memory and processes)"
		}
		return fmt.Errorf("%s is %s, not running%s", termsafe.Clean(ref), sb.State, hint)
	}
	tty := isTerminal(os.Stdin) && isTerminal(os.Stdout)
	rows, cols := termSize(os.Stdout)
	req := api.RunRequest{Argv: argv, Cwd: cwd, Tty: tty, Rows: rows, Cols: cols}
	if tty {
		// The guest's programs draw for the terminal they are told they have.
		term := os.Getenv("TERM")
		if term == "" || term == "dumb" {
			term = "xterm-256color"
		}
		req.Env = map[string]string{"TERM": term}
	}
	p, err := c.StartProcess(ctx, sb.ID, req)
	if err != nil {
		return err
	}
	code, err := attach(ctx, c, sb.ID, p.PID, tty, true)
	if errors.Is(err, errDetached) {
		return nil
	}
	if err != nil {
		return err
	}
	if code != 0 {
		return exitError{code}
	}
	return nil
}

func newShellCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "shell SANDBOX",
		Short: "Open an interactive shell in a running sandbox",
		Long: "Starts bash (or sh, where the image has no bash) in a running sandbox, in the\n" +
			"sandbox user's home, and connects your terminal to it. Exiting the shell leaves\n" +
			"the sandbox running. SANDBOX is an id or a name, as `sandbox-cli list` shows.",
		Example: "  sandbox-cli shell demo\n  sandbox-cli shell sbx_0123456789abcdef --context box",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connect(cmd.Context(), ctxFlag, args[0], shellArgv, agenthome.GuestHome)
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	return cmd
}

func newExecCmd() *cobra.Command {
	var ctxFlag, cwd string
	cmd := &cobra.Command{
		Use:   "exec SANDBOX -- COMMAND [ARGS...]",
		Short: "Run a command in a running sandbox",
		Long: "Runs COMMAND in a running sandbox, on a terminal when yours is one, and exits\n" +
			"with its status. The sandbox keeps running. COMMAND starts in the sandbox\n" +
			"user's home unless --workdir says otherwise.",
		Example: "  sandbox-cli exec demo -- git clone https://github.com/you/app\n  sandbox-cli exec demo --workdir /sandbox/home/app -- make test",
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return connect(cmd.Context(), ctxFlag, args[0], args[1:], cwd)
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().StringVarP(&cwd, "workdir", "w", agenthome.GuestHome, "directory to run COMMAND in")
	return cmd
}
