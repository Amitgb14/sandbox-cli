// Command sandbox-gateway is one address in front of many sandboxd nodes
// (package gateway). It speaks Sandbox API v1 with users' API keys, decides
// where each sandbox goes, and forwards every call to the node that holds
// the sandbox with that node's own token.
//
//	sandbox-gateway serve --listen ADDR --state FILE [--tls-cert C --tls-key K] [--node-config nodes.yaml] …
//	sandbox-gateway keys create --user U [--tenant T] --scope S…   prints the secret once
//	sandbox-gateway keys list | keys revoke ID
//	sandbox-gateway nodes add NAME ENDPOINT [--token-file …] | nodes list | nodes remove NAME
//
// keys and nodes work on the state file directly, so the first admin key is
// made before the gateway serves. While it serves it holds the file, and they
// refuse: a change made beside a running gateway would be overwritten by it,
// and a revoked key would come back. Change a serving gateway through its
// admin API instead.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/gateway"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
	"github.com/Amitgb14/sandbox-cli/internal/version"
)

func main() {
	if err := newRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-gateway: "+err.Error())
		os.Exit(1)
	}
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "sandbox-gateway",
		Short:         "One Sandbox API endpoint in front of many sandboxd nodes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	var state string
	root.PersistentFlags().StringVar(&state, "state", "", "the gateway's state file (keys, owners, nodes); required")
	root.AddCommand(newServe(&state), newKeys(&state), newNodes(&state))
	return root
}

func openState(path string) (*gateway.FileStore, error) {
	if path == "" {
		return nil, errors.New("--state is required")
	}
	return gateway.OpenFileStore(path)
}

// --- serve ----------------------------------------------------------------------

type serveOptions struct {
	listen, tlsCert, tlsKey, nodeConfig, nodeFilesDir string
	sshListen, sshHostKey, sshPublicHost              string
	sshPublicPort                                     int
	quota                                             gateway.Quota
	pollInterval                                      time.Duration
	corsOrigins                                       []string
	metricsListen, auditLog                           string
	nodeLostAfter                                     time.Duration
	// metricsReady, when set, is told the metrics address (tests).
	metricsReady func(string)
}

func newServe(state *string) *cobra.Command {
	var o serveOptions
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the API (and SSH, with --ssh-listen)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return serve(ctx, *state, o, func(format string, a ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-gateway: "+format+"\n", a...)
			}, nil)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.listen, "listen", "127.0.0.1:7443", "host:port to serve the API on; a non-loopback address needs --tls-cert and --tls-key")
	f.StringVar(&o.tlsCert, "tls-cert", "", "TLS certificate (PEM)")
	f.StringVar(&o.tlsKey, "tls-key", "", "TLS private key (PEM)")
	f.StringVar(&o.nodeConfig, "node-config", "", "YAML file listing nodes (nodes: [{name, endpoint, token_file, ca_file, cert_file, key_file}])")
	f.StringVar(&o.nodeFilesDir, "node-files-dir", "", "the only directory whose files a node added through the admin API may name (default: nodes/ beside the state file)")
	f.StringVar(&o.sshListen, "ssh-listen", "", "host:port for the SSH server; off when empty")
	f.StringVar(&o.sshHostKey, "ssh-host-key", "", "the SSH host key, created when missing (default: ssh_host_ed25519_key beside the state file)")
	f.StringVar(&o.sshPublicHost, "ssh-public-host", "", "the host clients are told to connect to for SSH (default: the --ssh-listen host)")
	f.IntVar(&o.sshPublicPort, "ssh-public-port", 0, "the port clients are told to connect to for SSH (default: the --ssh-listen port)")
	f.IntVar(&o.quota.Sandboxes, "quota-sandboxes", 0, "most sandboxes one tenant may hold at once; 0 is unlimited")
	f.Float64Var(&o.quota.CPUs, "quota-cpus", 0, "most CPUs one tenant's sandboxes may hold; 0 is unlimited")
	f.IntVar(&o.quota.MemoryMB, "quota-memory-mb", 0, "most memory (MB) one tenant's sandboxes may hold; 0 is unlimited")
	f.DurationVar(&o.pollInterval, "poll-interval", 5*time.Second, "how often each node is asked for its status")
	f.StringArrayVar(&o.corsOrigins, "cors-origin", nil, "a browser origin allowed to call the API (repeatable)")
	f.StringVar(&o.metricsListen, "metrics-listen", "", "loopback host:port to serve Prometheus metrics on, without a credential; off when empty")
	f.StringVar(&o.auditLog, "audit-log", "", `who did what, as JSONL (default: audit/gateway.jsonl beside the state file; "none" keeps no log)`)
	f.DurationVar(&o.nodeLostAfter, "node-lost-after", 5*time.Minute, "how long a node may not answer before its sandboxes are reported lost and stop counting against quotas")
	return cmd
}

// serve runs the gateway until ctx ends. ready, when set, is told the API's
// address once it is listening (tests).
func serve(ctx context.Context, statePath string, o serveOptions, logf func(string, ...any), ready func(string)) error {
	if o.quota.Sandboxes < 0 || o.quota.CPUs < 0 || o.quota.MemoryMB < 0 {
		return errors.New("a quota cannot be negative")
	}
	if (o.tlsCert == "") != (o.tlsKey == "") {
		return errors.New("--tls-cert and --tls-key go together")
	}
	static, err := loadNodeConfig(o.nodeConfig)
	if err != nil {
		return err
	}
	st, err := openState(statePath)
	if err != nil {
		return err
	}
	defer st.Close()
	if o.nodeFilesDir == "" {
		o.nodeFilesDir = filepath.Join(filepath.Dir(statePath), "nodes")
	}
	if err := os.MkdirAll(o.nodeFilesDir, 0o700); err != nil {
		return err
	}
	// On by default, as sandboxd's: a record of who did what cannot be
	// turned on after the fact.
	auditPath := o.auditLog
	switch auditPath {
	case "":
		auditPath = filepath.Join(filepath.Dir(statePath), "audit", "gateway.jsonl")
	case "none":
		auditPath = ""
	}
	auditLog := gateway.NewAuditLog(auditPath)
	g, err := gateway.New(gateway.Config{
		Store: st, StaticNodes: static, PollInterval: o.pollInterval, Quota: o.quota,
		NodeFilesDir: o.nodeFilesDir, CORSOrigins: o.corsOrigins, Logf: logf,
		NodeLostAfter: o.nodeLostAfter, Audit: auditLog,
	})
	if err != nil {
		return err
	}
	var metricsLn net.Listener
	if o.metricsListen != "" {
		if metricsLn, err = metrics.Listen(o.metricsListen); err != nil {
			return err
		}
		defer metricsLn.Close()
	}

	// The SSH server is built before the API listens, so a gateway asked to
	// serve SSH and unable to does not start at all.
	var sshSrv *gateway.SSHServer
	var sshLn net.Listener
	if o.sshListen != "" {
		cfg, err := sshConfig(statePath, o)
		if err != nil {
			return err
		}
		cfg.Store, cfg.Router, cfg.Logf = st, g, logf
		cfg.Audit, cfg.Metrics = auditLog, g.SSHMetrics()
		if sshSrv, err = gateway.NewSSHServer(cfg); err != nil {
			return fmt.Errorf("ssh: %w", err)
		}
		if sshLn, err = net.Listen("tcp", o.sshListen); err != nil {
			sshSrv.Close()
			return fmt.Errorf("ssh: %w", err)
		}
		g.SetSSH(sshSrv)
	}

	ln, where, err := openListener(o.listen, o.tlsCert != "")
	if err != nil {
		if sshSrv != nil {
			sshLn.Close()
			sshSrv.Close()
		}
		return err
	}
	if o.tlsCert != "" {
		cert, err := tls.LoadX509KeyPair(o.tlsCert, o.tlsKey)
		if err != nil {
			ln.Close()
			return fmt.Errorf("tls: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
		where = "https://" + where
	} else {
		where = "http://" + where
	}

	g.Start(ctx)
	defer g.Close()
	srv := &http.Server{
		Handler: g.Handler(),
		// Output streams and attached terminals are long-lived; no write timeout.
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 3)
	go func() { errc <- srv.Serve(ln) }()
	if sshSrv != nil {
		go func() { errc <- fmt.Errorf("ssh: %w", sshSrv.Serve(sshLn)) }()
		logf("serving SSH on %s", sshLn.Addr())
	}
	var msrv *http.Server
	if metricsLn != nil {
		msrv = &http.Server{Handler: g.MetricsHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errc <- fmt.Errorf("metrics: %w", msrv.Serve(metricsLn)) }()
		logf("serving metrics on http://%s/metrics", metricsLn.Addr())
		if o.metricsReady != nil {
			o.metricsReady(metricsLn.Addr().String())
		}
	}
	if auditPath != "" {
		logf("audit log %s", auditPath)
	} else {
		logf("audit log off")
	}
	healthy := 0
	for _, n := range g.Nodes() {
		if n.Healthy {
			healthy++
		}
	}
	logf("%s serving API %s on %s; %d nodes, %d answering; %d keys", version.Version, api.Version, where,
		len(g.Nodes()), healthy, len(st.Keys()))
	if ready != nil {
		ready(where)
	}
	select {
	case err = <-errc:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if serr := srv.Shutdown(shutdown); err == nil {
		err = serr
	}
	if msrv != nil {
		msrv.Close()
	}
	if sshSrv != nil {
		sshSrv.Close()
	}
	return err
}

func sshConfig(statePath string, o serveOptions) (gateway.SSHConfig, error) {
	host, port, err := net.SplitHostPort(o.sshListen)
	if err != nil {
		return gateway.SSHConfig{}, fmt.Errorf("--ssh-listen %q: %w", o.sshListen, err)
	}
	cfg := gateway.SSHConfig{HostKeyFile: o.sshHostKey, Host: o.sshPublicHost, Port: o.sshPublicPort}
	if cfg.HostKeyFile == "" {
		cfg.HostKeyFile = filepath.Join(filepath.Dir(statePath), "ssh_host_ed25519_key")
	}
	if cfg.Host == "" {
		if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
			return gateway.SSHConfig{}, errors.New("--ssh-listen names no host clients can reach; set --ssh-public-host")
		}
		cfg.Host = host
	}
	if cfg.Port == 0 {
		if cfg.Port, err = strconv.Atoi(port); err != nil || cfg.Port == 0 {
			return gateway.SSHConfig{}, errors.New("--ssh-listen names no fixed port; set --ssh-public-port")
		}
	}
	return cfg, nil
}

// openListener applies sandboxd's rule for a TCP address: one other
// machines can reach needs TLS, since every request carries an API key and a
// key on a network in the clear is anyone's. Loopback may go without — behind
// a proxy on the same machine that terminates TLS.
func openListener(addr string, haveTLS bool) (net.Listener, string, error) {
	if strings.HasPrefix(addr, "unix://") {
		return nil, "", fmt.Errorf("--listen %q: the gateway listens on TCP", addr)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, "", fmt.Errorf("--listen %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback && !haveTLS {
		return nil, "", fmt.Errorf("--listen %s is reachable from other machines; refusing to serve it without --tls-cert and --tls-key", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	return ln, ln.Addr().String(), nil
}

// nodeFile is the node config file's shape.
type nodeFile struct {
	Nodes []struct {
		Name      string `yaml:"name"`
		Endpoint  string `yaml:"endpoint"`
		TokenFile string `yaml:"token_file"`
		CAFile    string `yaml:"ca_file"`
		CertFile  string `yaml:"cert_file"`
		KeyFile   string `yaml:"key_file"`
	} `yaml:"nodes"`
}

// loadNodeConfig reads the node config file. Unknown keys are refused: a
// misspelt ca_file would otherwise mean the system's CAs are trusted.
func loadNodeConfig(path string) ([]gateway.NodeConfig, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f nodeFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out []gateway.NodeConfig
	for _, n := range f.Nodes {
		c := gateway.NodeConfig{Name: n.Name, Endpoint: n.Endpoint, TokenFile: n.TokenFile,
			CAFile: n.CAFile, CertFile: n.CertFile, KeyFile: n.KeyFile}
		if err := gateway.CheckNodeConfig(c); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out = append(out, c)
	}
	return out, nil
}

// --- keys -----------------------------------------------------------------------

func newKeys(state *string) *cobra.Command {
	cmd := &cobra.Command{Use: "keys", Short: "Issue, list and revoke API keys in the state file"}
	var user, tenant string
	var scopes []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Issue an API key; its secret is printed once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			secret, k, err := st.CreateKey(user, tenant, scopes)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id:     %s\nuser:   %s\ntenant: %s\nscopes: %s\nsecret: %s\n", k.ID, k.User, k.Tenant, strings.Join(k.Scopes, ","), secret)
			fmt.Fprintln(cmd.ErrOrStderr(), "The secret is shown once and stored only as a hash; keep it now.")
			return nil
		},
	}
	create.Flags().StringVar(&user, "user", "", "the user the key acts as; required")
	create.Flags().StringVar(&tenant, "tenant", "", "the user's tenant (quotas are per tenant)")
	create.Flags().StringArrayVar(&scopes, "scope", nil, "a scope: "+strings.Join(gateway.AllScopes, ", ")+" (repeatable)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List API keys (never their secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tUSER\tTENANT\tSCOPES\tCREATED\tSTATE")
			for _, k := range st.Keys() {
				state := "active"
				if k.Revoked {
					state = "revoked"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.User, k.Tenant, strings.Join(k.Scopes, ","),
					k.Created.Format(time.RFC3339), state)
			}
			return tw.Flush()
		},
	}
	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.RevokeKey(args[0]); err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(create, list, revoke)
	return cmd
}

// --- nodes ----------------------------------------------------------------------

func newNodes(state *string) *cobra.Command {
	cmd := &cobra.Command{Use: "nodes", Short: "Add, list and remove the nodes in the state file"}
	var cfg gateway.NodeConfig
	add := &cobra.Command{
		Use:   "add NAME ENDPOINT",
		Short: "Add a node (https://host:port, or unix:///path on this machine)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Name, cfg.Endpoint = args[0], args[1]
			// Absolute, so the gateway finds them from wherever it is started.
			for _, p := range []*string{&cfg.TokenFile, &cfg.CAFile, &cfg.CertFile, &cfg.KeyFile} {
				if *p != "" {
					abs, err := filepath.Abs(*p)
					if err != nil {
						return err
					}
					*p = abs
				}
			}
			// Building the client checks the endpoint and reads every file,
			// so a node that cannot be reached as configured is refused now
			// rather than at the next start.
			if _, err := gateway.NewNodeClient(cfg); err != nil {
				return err
			}
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.PutNode(cfg); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "added node %s (%s)\n", cfg.Name, cfg.Endpoint)
			return nil
		},
	}
	add.Flags().StringVar(&cfg.TokenFile, "token-file", "", "file holding the node's bearer token (mode 0600)")
	add.Flags().StringVar(&cfg.CAFile, "ca-file", "", "CA that signed the node's certificate (instead of the system's)")
	add.Flags().StringVar(&cfg.CertFile, "cert-file", "", "the gateway's client certificate, for mutual TLS")
	add.Flags().StringVar(&cfg.KeyFile, "key-file", "", "the gateway's client key (mode 0600)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List the nodes in the state file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tENDPOINT\tTOKEN FILE\tCA FILE\tCLIENT CERT")
			for _, n := range st.Nodes() {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", n.Name, n.Endpoint, dash(n.TokenFile), dash(n.CAFile), dash(n.CertFile))
			}
			return tw.Flush()
		},
	}
	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a node; its sandboxes' owners are kept, for if it comes back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.RemoveNode(args[0]); err != nil {
				return fmt.Errorf("node %s: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed node %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(add, list, remove)
	return cmd
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
