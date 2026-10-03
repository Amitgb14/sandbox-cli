package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/studio"
	"github.com/Amitgb14/sandbox-cli/internal/version"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

func newStudioCmd() *cobra.Command {
	var ctxFlag, uiDir string
	var port int
	cmd := &cobra.Command{
		Use:   "studio",
		Short: "Open Studio: the browser view of your sandboxes, runs and returned work",
		Long: "Serves Studio on a loopback port and prints the address to open, which carries a\n" +
			"token made for this launch: anyone else on this machine can reach the port, and\n" +
			"the token is what keeps them out. Studio talks to the sandboxd of the current\n" +
			"context (or --context) and holds its token itself; the browser never sees it.\n\n" +
			"Run from a git checkout, the repository is registered so runs can be launched\n" +
			"on it. Ctrl-C stops Studio; sandboxes it started keep running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, ctxName, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			tok := make([]byte, 24)
			if _, err := rand.Read(tok); err != nil {
				return err
			}
			srv := &studio.Server{
				Client: c, Context: ctxName, Token: hex.EncodeToString(tok), UI: studio.EmbeddedUI(),
				Launch:    studioLauncher(ctxName),
				ReposFile: filepath.Join(workspace.ConfigDir(), "studio", "repos.json"),
				Version:   version.Version,
				Logf: func(format string, a ...any) {
					fmt.Fprintf(os.Stderr, "studio: "+format+"\n", a...)
				},
			}
			if uiDir != "" {
				// A UI built from source, served from disk: how Studio's
				// frontend is worked on without rebuilding sandbox-cli.
				if _, err := os.Stat(filepath.Join(uiDir, "index.html")); err != nil {
					return fmt.Errorf("--ui-dir %s: no index.html (npm run build in studio/ writes studio/out)", uiDir)
				}
				srv.UI = os.DirFS(uiDir)
			}
			if wd, err := os.Getwd(); err == nil {
				if rp, ok := srv.RegisterRepo(wd); ok {
					fmt.Fprintf(cmd.ErrOrStderr(), "studio: repository %s\n", rp.Path)
				}
			}
			// Loopback only, and no flag to change it: Studio holds a token
			// that can start sandboxes, and the network is not where it goes.
			ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return fmt.Errorf("listening on 127.0.0.1:%d: %w", port, err)
			}
			hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			fmt.Fprintf(cmd.OutOrStdout(), "Studio: http://%s/#token=%s\n", ln.Addr(), srv.Token)
			fmt.Fprintf(cmd.ErrOrStderr(), "studio: context %s · Ctrl-C to stop\n", ctxName)
			go func() {
				<-cmd.Context().Done()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(ctx)
			}()
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd Studio talks to")
	cmd.Flags().IntVar(&port, "port", 7080, "loopback port to serve on (0: any free one)")
	cmd.Flags().StringVar(&uiDir, "ui-dir", "", "serve the UI from this directory instead of the one built in (studio/out, when working on it)")
	return cmd
}

// studioLauncher turns a Studio launch into the run the CLI would make: a
// detached `sandbox-cli run` or `sandbox-cli agent`, with every rule that
// comes with it — the user's config and its trust layering, the profile, the
// agent's environment and login, the git identity, checkpoints aside (a
// detached run has nobody to drive them).
func studioLauncher(ctxName string) studio.Launcher {
	return func(ctx context.Context, repoPath string, req studio.LaunchRequest) (studio.LaunchResult, error) {
		rf := &runFlags{context: ctxName, project: repoPath, detach: true, name: req.Name,
			network: req.Network, allow: req.Allow, git: req.Git, profile: req.Profile}
		for k, v := range req.Labels {
			rf.labels = append(rf.labels, k+"="+v)
		}
		for _, v := range req.Volumes {
			spec := v.Name + ":" + v.Path
			if v.ReadOnly {
				spec += ":ro"
			}
			rf.volumes = append(rf.volumes, spec)
		}
		rs := runSpec{result: &runResult{}, labels: map[string]string{"studio": "1"}}
		switch {
		case req.Agent != "" && req.Console:
			d, ok := agents.LookupInteractive(req.Agent)
			if !ok {
				return studio.LaunchResult{}, fmt.Errorf("unknown agent %q", req.Agent)
			}
			if req.Prompt != "" && !d.CanSeedConsole() {
				return studio.LaunchResult{}, fmt.Errorf("%s cannot be given its first turn on the command line; start the console without a prompt and type it", d.Name)
			}
			rs.agent, rs.argv, rs.console = &d, d.Console(req.Prompt, false), true
			rs.rows, rs.cols = req.Rows, req.Cols
			if rs.rows == 0 || rs.cols == 0 {
				rs.rows, rs.cols = 30, 110
			}
		case req.Agent != "":
			d, ok := agents.Lookup(req.Agent)
			if !ok {
				return studio.LaunchResult{}, fmt.Errorf("%s has no verified headless mode", req.Agent)
			}
			rs.agent, rs.argv = &d, d.Autonomous(req.Prompt, nil)
		default:
			rs.argv = req.Command
		}
		if _, err := execute(ctx, rf, rs); err != nil {
			return studio.LaunchResult{}, err
		}
		if rs.result.sandbox == "" {
			return studio.LaunchResult{}, errors.New("the run did not start a sandbox")
		}
		return studio.LaunchResult{Sandbox: rs.result.sandbox, PID: rs.result.pid}, nil
	}
}
