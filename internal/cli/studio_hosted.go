package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/studio"
	"github.com/Amitgb14/sandbox-cli/internal/version"
)

func newStudioHostCmd() *cobra.Command {
	var (
		gateway, publicURL, caFile, listen, tlsCert, tlsKey, uiDir string
		allowedHosts                                               []string
		idle, maxAge                                               time.Duration
	)
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Serve Studio to many users, each signed in with their own gateway key",
		Long: "Serves Studio on a public address for the users of a gateway. Each user signs in\n" +
			"once with their own API key, usually by opening an invite link\n" +
			"(https://studio.example.com/#key=sgk_…, printed by sandbox-gateway keys create\n" +
			"--invite-url), and from then on acts as themselves: the gateway decides what\n" +
			"they see, exactly as for their own CLI.\n\n" +
			"It holds no credential of its own and reads nothing from this machine: no\n" +
			"context, no agent logins, no environment, no config. Unattended agent runs go\n" +
			"through jobs, with the user's API key stored as a secret. Admin keys are refused;\n" +
			"use sandbox-cli studio on your own machine for those.\n\n" +
			"Sessions are kept in memory: a restart signs everyone out, and they open their\n" +
			"invite link again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if gateway == "" || publicURL == "" {
				return errors.New("--gateway and --public-url are required")
			}
			hc := &http.Client{}
			if caFile != "" {
				pem, err := os.ReadFile(caFile)
				if err != nil {
					return fmt.Errorf("--ca: %w", err)
				}
				c, err := api.NewClientWithCA(gateway, "", pem)
				if err != nil {
					return fmt.Errorf("--ca: %w", err)
				}
				_, rt, _ := c.Transport()
				hc.Transport = rt
			}
			h := &studio.Hosted{
				Gateway: strings.TrimSuffix(gateway, "/"), HTTP: hc,
				PublicOrigin: strings.TrimSuffix(publicURL, "/"), AllowedHosts: allowedHosts,
				SessionIdle: idle, SessionMaxAge: maxAge,
			}
			if err := h.Validate(); err != nil {
				return err
			}
			if err := gatewayHealthy(cmd.Context(), hc, h.Gateway); err != nil {
				return err
			}
			// A session cookie and the keys behind it cross this listener:
			// in the clear only on loopback, with a proxy in front doing TLS.
			host, _, err := net.SplitHostPort(listen)
			if err != nil {
				return fmt.Errorf("--listen %q: want host:port", listen)
			}
			if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && host != "localhost" && (tlsCert == "" || tlsKey == "") {
				return fmt.Errorf("--listen %s is not loopback: give --tls-cert and --tls-key, or listen on loopback behind a TLS proxy", listen)
			}
			srv := &studio.Server{
				Hosted: h, UI: studio.EmbeddedUI(), Launch: hostedLauncher(),
				Version: version.Version,
				Logf: func(format string, a ...any) {
					fmt.Fprintf(os.Stderr, "studio: "+format+"\n", a...)
				},
			}
			if uiDir != "" {
				if _, err := os.Stat(filepath.Join(uiDir, "index.html")); err != nil {
					return fmt.Errorf("--ui-dir %s: no index.html (make studio-hosted writes studio/out-hosted)", uiDir)
				}
				srv.UI = os.DirFS(uiDir)
			}
			ln, err := net.Listen("tcp", listen)
			if err != nil {
				return err
			}
			hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			if tlsCert != "" {
				hs.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Studio (hosted): %s, listening on %s, gateway %s\n", h.PublicOrigin, ln.Addr(), h.Gateway)
			fmt.Fprintf(cmd.ErrOrStderr(), "studio: invite links are %s/#key=<the user's key> · Ctrl-C to stop\n", h.PublicOrigin)
			go func() {
				<-cmd.Context().Done()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(ctx)
			}()
			if tlsCert != "" {
				err = hs.ServeTLS(ln, tlsCert, tlsKey)
			} else {
				err = hs.Serve(ln)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&gateway, "gateway", "", "the gateway's URL (http:// on loopback only)")
	f.StringVar(&publicURL, "public-url", "", "the address users open, https://host[:port] (http:// on loopback only)")
	f.StringVar(&caFile, "ca", "", "CA (PEM) the gateway's certificate is signed by, for a private CA")
	f.StringVar(&listen, "listen", "127.0.0.1:7080", "host:port to serve on; a non-loopback address needs --tls-cert and --tls-key")
	f.StringVar(&tlsCert, "tls-cert", "", "TLS certificate (PEM)")
	f.StringVar(&tlsKey, "tls-key", "", "TLS private key (PEM)")
	f.StringArrayVar(&allowedHosts, "allowed-host", nil, "a further Host header to answer, for a proxy that rewrites it (repeatable)")
	f.StringVar(&uiDir, "ui-dir", "", "serve the UI from this directory (studio/out-hosted, from make studio-hosted)")
	f.DurationVar(&idle, "session-idle", 12*time.Hour, "end a session not used for this long")
	f.DurationVar(&maxAge, "session-max-age", 7*24*time.Hour, "end a session this long after sign-in")
	return cmd
}

// gatewayHealthy is a start-up check that the gateway answers, so a typo in
// --gateway fails here rather than at every user's first sign-in.
func gatewayHealthy(ctx context.Context, hc *http.Client, gateway string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, gateway+"/v1/health", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("the gateway at %s does not answer: %w", gateway, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the gateway at %s answered %s to /v1/health", gateway, resp.Status)
	}
	return nil
}

// hostedLauncher starts a Playground run for a hosted Studio's user, with
// that user's client and nothing of this machine's.
//
// It makes the API calls runSandbox makes, and leaves out every step that
// reads the host: the user's and the project's config (applyConfig), the
// agent's environment allowlist forwarded from this process (buildEnv), and
// the agent logins saved here (RestoreLogin, SaveLogin). Each of those would
// hand one person's machine — its API keys, its OAuth logins — to every user
// of the Studio. What remains is what the request names, under the server's
// policy, which applies to it as to any client's.
func hostedLauncher() studio.Launcher {
	return func(ctx context.Context, c *api.Client, req studio.LaunchRequest) (studio.LaunchResult, error) {
		if c == nil {
			return studio.LaunchResult{}, errors.New("no client for this request")
		}
		if req.Profile != "" {
			return studio.LaunchResult{}, errors.New("profiles are local configuration; a hosted Studio runs under the server's policy only")
		}
		var (
			agent      *agents.Descriptor
			argv       []string
			tty        bool
			rows, cols uint16
		)
		switch {
		case req.Agent != "" && req.Console:
			d, ok := agents.LookupInteractive(req.Agent)
			if !ok {
				return studio.LaunchResult{}, fmt.Errorf("unknown agent %q", req.Agent)
			}
			if req.Prompt != "" && !d.CanSeedConsole() {
				return studio.LaunchResult{}, fmt.Errorf("%s cannot be given its first turn on the command line; start the console without a prompt and type it", d.Name)
			}
			agent, argv, tty = &d, d.Console(req.Prompt, false), true
			rows, cols = req.Rows, req.Cols
			if rows == 0 || cols == 0 {
				rows, cols = 30, 110
			}
		case req.Agent != "":
			return studio.LaunchResult{}, errors.New("run an agent unattended as a job, with its API key stored as a secret")
		default:
			argv = req.Command
		}

		caps, err := c.Capabilities(ctx)
		if err != nil {
			return studio.LaunchResult{}, err
		}
		cr := api.CreateSandboxRequest{
			Name: req.Name, Image: req.Image, SnapshotID: req.Snapshot,
			SnapshotEverySecs: req.SnapshotEverySecs, SnapshotKeep: req.SnapshotKeep,
			Env: map[string]string{},
		}
		// The agent's constant settings only. Its EnvAllow names are values
		// this process would read from its own environment: never here.
		if agent != nil {
			for _, kv := range agent.Env {
				if k, v, ok := strings.Cut(kv, "="); ok {
					cr.Env[k] = v
				}
			}
		}
		for k, v := range guestGitIdentity {
			cr.Env[k] = v
		}
		var labels []string
		for k, v := range req.Labels {
			labels = append(labels, k+"="+v)
		}
		if cr.Labels, err = buildLabels(labels, runSpec{agent: agent, labels: map[string]string{"studio": "1"}}); err != nil {
			return studio.LaunchResult{}, err
		}
		cr.Volumes = append(cr.Volumes, req.Volumes...)
		// The tools volume is built over the API, on the endpoint, for the
		// user who asked: nothing of this machine's goes into it.
		if agent != nil && req.Snapshot == "" && !mountsNear(cr.Volumes, agents.ToolsDir) {
			if m := agenthome.AgentTools(ctx, c, caps, *agent, func(string, ...any) {}); m != nil {
				cr.Volumes = append(cr.Volumes, *m)
			}
		}
		if cr.Network, err = hostedNetwork(req, caps, agent); err != nil {
			return studio.LaunchResult{}, err
		}

		sb, err := c.CreateSandbox(ctx, cr)
		if err != nil {
			return studio.LaunchResult{}, err
		}
		p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: argv, Cwd: agenthome.GuestHome, Tty: tty, Rows: rows, Cols: cols})
		if err != nil {
			_ = c.TerminateSandbox(context.WithoutCancel(ctx), sb.ID)
			return studio.LaunchResult{}, err
		}
		return studio.LaunchResult{Sandbox: sb.ID, PID: p.PID}, nil
	}
}

// hostedNetwork is the network a hosted run asks for: the built-in defaults
// with the request's mode and names on top, resolved the way every other run
// is (resolveNetwork), and never a config file of this machine's.
func hostedNetwork(req studio.LaunchRequest, caps api.Capabilities, agent *agents.Descriptor) (*api.NetworkPolicy, error) {
	ov, err := (&runFlags{network: req.Network, allow: req.Allow}).overrides()
	if err != nil {
		return nil, err
	}
	cfg := policy.Default()
	if len(req.Allow) > 0 && cfg.Network.Mode != "none" {
		cfg.Network.Mode = "allowlist"
	}
	if ov.NetworkMode != "" {
		cfg.Network.Mode = ov.NetworkMode
	}
	host := ""
	if agent != nil {
		host = agent.ProviderHost
	}
	n, err := resolveNetwork(cfg, req.Allow, nil, caps, host)
	if err != nil {
		return nil, err
	}
	return withAgentAPI(n, agent, caps), nil
}
