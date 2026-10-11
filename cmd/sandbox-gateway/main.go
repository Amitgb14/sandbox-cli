// Command sandbox-gateway is one address in front of many sandboxd nodes
// (package gateway). It speaks Sandbox API v1 with users' API keys, decides
// where each sandbox goes, and forwards every call to the node that holds
// the sandbox with that node's own token.
//
//	sandbox-gateway serve --listen ADDR --state FILE [--tls-cert C --tls-key K] [--node-config nodes.yaml] …
//	                      [--router-listen ADDR --router-domain apps.example.com [--router-tls-cert C --router-tls-key K]]
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
	"slices"
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
	var state, stateDir string
	pf := root.PersistentFlags()
	pf.StringVar(&stateDir, "state-dir", "", "the gateway's directory: its SQLite database (gateway.db), SSH host key, audit log, jobs and node files")
	pf.StringVar(&state, "state", "", "a file in the gateway's directory, as --state-dir is its directory (the state file of a gateway before databases)")
	pf.StringVar(&storeFlags.kind, "store", "sqlite", "where the gateway keeps its state: sqlite (one gateway) or postgres (one or more)")
	pf.StringVar(&storeFlags.path, "store-path", "", "the SQLite database (default: gateway.db in the gateway's directory)")
	pf.StringVar(&storeFlags.dsnFile, "store-dsn-file", "", "with --store postgres: a file (mode 0600) holding the database's address, postgres://… with sslmode=verify-full unless it is on this machine")
	pf.BoolVar(&storeFlags.migrate, "store-migrate", false, "with --store postgres: let a schema migration move the database's rows into a new shape; back the database up (pg_dump) first")
	root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if stateDir != "" {
			if state != "" {
				return errors.New("--state and --state-dir: give one")
			}
			state = filepath.Join(stateDir, "state.json")
		}
		return nil
	}
	root.AddCommand(newServe(&state), newKeys(&state), newNodes(&state), newPolicies(&state), newStore(&state))
	return root
}

// storeFlags are the root's flags naming the gateway's store.
var storeFlags storeOptions

type storeOptions struct {
	kind, path, dsnFile string
	// migrate lets a migration move a PostgreSQL database's rows (one that
	// changes their shape), once it has been backed up.
	migrate bool
}

func (o storeOptions) kindOrDefault() string {
	if o.kind == "" {
		return "sqlite"
	}
	return o.kind
}

// openState opens the gateway's store: the SQLite database in the
// directory statePath is in, or the PostgreSQL database --store-dsn-file
// names. A state file left by a gateway from before databases, beside a
// database that holds nothing, is refused with the command that imports it
// — the gateway never starts empty next to its own old state.
func openState(statePath string) (gateway.Store, error) {
	if statePath == "" {
		return nil, errors.New("--state-dir is required")
	}
	st, err := openStoreAt(statePath)
	if err != nil {
		return nil, err
	}
	if legacy := legacyState(statePath); legacy != "" {
		empty, err := storeIsEmpty(st)
		if err != nil {
			st.Close()
			return nil, err
		}
		if empty {
			st.Close()
			return nil, fmt.Errorf("%s holds this gateway's state from before databases, and the database is empty: import it first with\n  sandbox-gateway --state-dir %s %s store import --from %s",
				legacy, filepath.Dir(statePath), storeArgs(), legacy)
		}
	}
	return st, nil
}

// openStoreAt opens the store without the check for an old state file:
// what store import itself uses.
func openStoreAt(statePath string) (gateway.Store, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch storeFlags.kind {
	case "", "sqlite":
		path := storeFlags.path
		if path == "" {
			path = filepath.Join(filepath.Dir(statePath), "gateway.db")
		}
		return gateway.OpenSQLite(ctx, path)
	case "postgres":
		if storeFlags.dsnFile == "" {
			return nil, errors.New("--store postgres needs --store-dsn-file")
		}
		dsn, err := gateway.ReadSecretFile(storeFlags.dsnFile)
		if err != nil {
			return nil, fmt.Errorf("--store-dsn-file: %w", err)
		}
		return gateway.OpenPostgres(ctx, dsn, gateway.OpenOptions{MigrateData: storeFlags.migrate})
	default:
		return nil, fmt.Errorf("--store %q: sqlite or postgres", storeFlags.kind)
	}
}

// legacyState is the state file a gateway from before databases left in
// statePath's directory, "" for none.
func legacyState(statePath string) string {
	for _, p := range []string{statePath, filepath.Join(filepath.Dir(statePath), "state.json")} {
		if strings.HasSuffix(p, ".json") {
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
				return p
			}
		}
	}
	return ""
}

// storeIsEmpty reports whether st holds no key and no node: a gateway that
// has never run on it.
func storeIsEmpty(st gateway.Store) (bool, error) {
	ctx := context.Background()
	keys, err := st.Keys(ctx)
	if err != nil {
		return false, err
	}
	nodes, err := st.Nodes(ctx)
	return len(keys) == 0 && len(nodes) == 0, err
}

// keyCount is how many keys the store holds, for the start-up line.
func keyCount(st gateway.Store) int {
	keys, _ := st.Keys(context.Background())
	return len(keys)
}

// storeArgs are the flags naming the store, to repeat in a command shown.
func storeArgs() string {
	switch storeFlags.kind {
	case "postgres":
		return "--store postgres --store-dsn-file " + storeFlags.dsnFile
	default:
		if storeFlags.path != "" {
			return "--store-path " + storeFlags.path
		}
		return ""
	}
}

// --- serve ----------------------------------------------------------------------

type serveOptions struct {
	listen, tlsCert, tlsKey, nodeConfig, nodeFilesDir string
	sshListen, sshHostKey, sshPublicHost              string
	sshPublicPort                                     int
	routerListen, routerDomain, routerCert, routerKey string
	routerScheme                                      string
	routerPort                                        int
	quota                                             gateway.Quota
	pollInterval                                      time.Duration
	corsOrigins                                       []string
	metricsListen, auditLog                           string
	nodeLostAfter                                     time.Duration
	// metricsReady, when set, is told the metrics address (tests).
	metricsReady            func(string)
	secretsKeyFile, jobsDir string
	jobRetention            time.Duration
	sandboxHistory          time.Duration
	notifyAllowPrivate      bool
	maxOrgsPerUser          int
	// Sending invites by email (gateway/mail.go).
	smtpHost, smtpUser, smtpPasswordFile, mailFrom, studioURL string
	smtpPort                                                  int
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
	f.StringVar(&o.routerListen, "router-listen", "", "host:port for the HTTP router that serves public services; off when empty. A non-loopback address needs --router-tls-cert and --router-tls-key")
	f.StringVar(&o.routerDomain, "router-domain", "", "the domain the router serves services under: <service>.DOMAIN, <service>--<tenant>.DOMAIN (one wildcard name)")
	f.StringVar(&o.routerCert, "router-tls-cert", "", "the router's TLS certificate (PEM), for *.DOMAIN")
	f.StringVar(&o.routerKey, "router-tls-key", "", "the router's TLS private key (PEM)")
	f.StringVar(&o.routerScheme, "router-public-scheme", "", "the scheme clients reach the router by, for services' URLs (default: https with --router-tls-cert, else http)")
	f.IntVar(&o.routerPort, "router-public-port", 0, "the port clients reach the router on, for services' URLs (default: the --router-listen port)")
	f.IntVar(&o.quota.Sandboxes, "quota-sandboxes", 0, "most sandboxes one tenant may hold at once; 0 is unlimited")
	f.Float64Var(&o.quota.CPUs, "quota-cpus", 0, "most CPUs one tenant's sandboxes may hold; 0 is unlimited")
	f.IntVar(&o.quota.MemoryMB, "quota-memory-mb", 0, "most memory (MB) one tenant's sandboxes may hold; 0 is unlimited")
	f.IntVar(&o.maxOrgsPerUser, "max-orgs-per-user", 10, "most organizations one user may create or own (each is a tenant with its own quota); 0 is unlimited")
	f.DurationVar(&o.pollInterval, "poll-interval", 5*time.Second, "how often each node is asked for its status")
	f.StringArrayVar(&o.corsOrigins, "cors-origin", nil, "a browser origin allowed to call the API (repeatable)")
	f.StringVar(&o.metricsListen, "metrics-listen", "", "loopback host:port to serve Prometheus metrics on, without a credential; off when empty")
	f.StringVar(&o.auditLog, "audit-log", "", `who did what, as JSONL (default: audit/gateway.jsonl beside the state file; "none" keeps no log)`)
	f.DurationVar(&o.nodeLostAfter, "node-lost-after", 5*time.Minute, "how long a node may not answer before its sandboxes are reported lost and stop counting against quotas")
	f.StringVar(&o.secretsKeyFile, "secrets-key-file", "", "32 random bytes (mode 0600) sealing secrets and jobs' environments; without it there are no secrets")
	f.StringVar(&o.jobsDir, "jobs-dir", "", "where jobs' runs keep output and files (default: jobs/ beside the state file)")
	f.DurationVar(&o.jobRetention, "job-retention", 24*time.Hour, "how long a finished job, and what it kept, is kept")
	f.DurationVar(&o.sandboxHistory, "sandbox-history", 30*24*time.Hour, "how long a terminated sandbox's details are kept in the database; 0 keeps them")
	f.BoolVar(&o.notifyAllowPrivate, "notify-allow-private", false, "let a job's notify URL reach loopback, private and link-local addresses (default: public addresses only)")
	f.StringVar(&o.smtpHost, "smtp-host", "", "an SMTP relay to email invites through; off when empty")
	f.IntVar(&o.smtpPort, "smtp-port", 587, "the relay's port: 465 is TLS from the start, any other needs STARTTLS")
	f.StringVar(&o.smtpUser, "smtp-user", "", "the relay's user name")
	f.StringVar(&o.smtpPasswordFile, "smtp-password-file", "", "a file (mode 0600) holding the relay's password or API key")
	f.StringVar(&o.mailFrom, "mail-from", "", `the sender of invites: "Sandbox Studio <invites@example.com>"`)
	f.StringVar(&o.studioURL, "studio-url", "", "hosted Studio's public address, which emailed links open: https://studio.example.com")
	return cmd
}

// serve runs the gateway until ctx ends. ready, when set, is told the API's
// address once it is listening (tests).
func serve(ctx context.Context, statePath string, o serveOptions, logf func(string, ...any), ready func(string)) error {
	if o.quota.Sandboxes < 0 || o.quota.CPUs < 0 || o.quota.MemoryMB < 0 {
		return errors.New("a quota cannot be negative")
	}
	if o.maxOrgsPerUser < 0 {
		return errors.New("--max-orgs-per-user cannot be negative")
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
	// One gateway serves a database at a time: a second one on the same
	// PostgreSQL waits here, listening on nothing, until the first stops
	// (gateway.HoldServing).
	serving, err := gateway.HoldServing(ctx, st, func() {
		logf("standby: another gateway serves this database; waiting to take over")
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer serving.Release()
	if o.nodeFilesDir == "" {
		o.nodeFilesDir = filepath.Join(filepath.Dir(statePath), "nodes")
	}
	if o.jobsDir == "" {
		o.jobsDir = filepath.Join(filepath.Dir(statePath), "jobs")
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
	var secretsKey []byte
	if o.secretsKeyFile != "" {
		if secretsKey, err = gateway.LoadSecretsKey(o.secretsKeyFile); err != nil {
			return err
		}
	}
	rcfg, err := routerConfig(o)
	if err != nil {
		return err
	}
	mailCfg, err := mailConfig(o)
	if err != nil {
		return err
	}
	g, err := gateway.New(gateway.Config{
		Store: st, StaticNodes: static, PollInterval: o.pollInterval, Quota: o.quota,
		NodeFilesDir: o.nodeFilesDir, CORSOrigins: o.corsOrigins, Logf: logf,
		NodeLostAfter: o.nodeLostAfter, Audit: auditLog,
		SecretsKey: secretsKey, JobsDir: o.jobsDir, JobRetention: o.jobRetention, SandboxHistory: o.sandboxHistory,
		NotifyAllowPrivate: o.notifyAllowPrivate,
		MaxOrgsPerUser:     o.maxOrgsPerUser,
		Router:             rcfg,
		Mail:               mailCfg,
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

	// The router, like SSH, listens before the API does, so a gateway asked
	// to route and unable to does not start at all. It is held to the API's
	// rule: off loopback, only with TLS.
	var routerLn net.Listener
	if o.routerListen != "" {
		if routerLn, err = listenTLS(o.routerListen, o.routerCert, o.routerKey, "router-"); err != nil {
			return fmt.Errorf("router: %w", err)
		}
		defer routerLn.Close()
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
	errc := make(chan error, 4)
	go func() { errc <- srv.Serve(ln) }()
	var routerSrv *http.Server
	if routerLn != nil {
		routerSrv = &http.Server{Handler: g.RouterHandler(), ReadHeaderTimeout: 10 * time.Second}
		go func() { errc <- fmt.Errorf("router: %w", routerSrv.Serve(routerLn)) }()
		logf("routing *.%s on %s", rcfg.Domain, routerLn.Addr())
	}
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
		len(g.Nodes()), healthy, keyCount(st))
	if ready != nil {
		ready(where)
	}
	select {
	case err = <-errc:
	case <-ctx.Done():
	case err = <-serving.Lost:
		// Another gateway may be serving already: stop taking requests
		// now, not after the graceful wait.
		logf("%v; stopping", err)
		srv.Close()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if serr := srv.Shutdown(shutdown); err == nil {
		err = serr
	}
	if msrv != nil {
		msrv.Close()
	}
	if routerSrv != nil {
		_ = routerSrv.Shutdown(shutdown)
	}
	if sshSrv != nil {
		sshSrv.Close()
	}
	return err
}

// routerConfig checks the router's flags and says where it serves.
func routerConfig(o serveOptions) (gateway.RouterConfig, error) {
	if o.routerListen == "" {
		if o.routerDomain != "" || o.routerCert != "" || o.routerKey != "" {
			return gateway.RouterConfig{}, errors.New("--router-domain and --router-tls-* need --router-listen")
		}
		return gateway.RouterConfig{}, nil
	}
	d := strings.ToLower(strings.TrimSuffix(o.routerDomain, "."))
	if d == "" || strings.HasPrefix(d, "*") || strings.ContainsAny(d, "/: ") || !strings.Contains(d, ".") {
		return gateway.RouterConfig{}, fmt.Errorf("--router-domain %q: a domain such as apps.example.com, which *.DOMAIN points at", o.routerDomain)
	}
	if (o.routerCert == "") != (o.routerKey == "") {
		return gateway.RouterConfig{}, errors.New("--router-tls-cert and --router-tls-key go together")
	}
	rc := gateway.RouterConfig{Domain: d, Scheme: o.routerScheme, Port: o.routerPort}
	if rc.Scheme == "" {
		rc.Scheme = "http"
		if o.routerCert != "" {
			rc.Scheme = "https"
		}
	}
	if rc.Scheme != "http" && rc.Scheme != "https" {
		return gateway.RouterConfig{}, fmt.Errorf("--router-public-scheme %q: http or https", rc.Scheme)
	}
	if rc.Port == 0 {
		_, port, err := net.SplitHostPort(o.routerListen)
		if err != nil {
			return gateway.RouterConfig{}, fmt.Errorf("--router-listen %q: %w", o.routerListen, err)
		}
		rc.Port, _ = strconv.Atoi(port)
	}
	return rc, nil
}

// listenTLS opens a listener under openListener's rule, in TLS when a
// certificate is given; its errors name the flags with prefix.
func listenTLS(addr, cert, key, prefix string) (net.Listener, error) {
	ln, _, err := openListener(addr, cert != "")
	if err != nil {
		return nil, errors.New(strings.NewReplacer("--listen", "--"+prefix+"listen", "--tls-", "--"+prefix+"tls-").Replace(err.Error()))
	}
	if cert == "" {
		return ln, nil
	}
	kp, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("tls: %w", err)
	}
	return tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{kp}, MinVersion: tls.VersionTLS12}), nil
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
	var user, tenant, invite, policy, expires string
	var scopes []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Issue an API key; its secret is printed once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Checked before the key exists, so a mistake here mints nothing.
			if invite != "" {
				var err error
				if invite, err = api.InviteBase(invite); err != nil {
					return err
				}
			}
			var expiresAt *time.Time
			if expires != "" {
				secs, err := api.ParseKeyExpiry(expires)
				if err != nil {
					return fmt.Errorf("--expires %w", err)
				}
				if secs > 0 {
					at := time.Now().Add(time.Duration(secs) * time.Second)
					expiresAt = &at
				}
			}
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			pols, err := st.Policies(ctx)
			if err != nil {
				return err
			}
			if policy != "" && !slices.ContainsFunc(pols, func(p api.UserPolicy) bool { return p.Name == policy }) {
				return fmt.Errorf("policy %s: no such policy (sandbox-gateway policies set)", policy)
			}
			secret, k, err := st.CreateKeyExpiring(ctx, user, tenant, scopes, expiresAt)
			if err != nil {
				return err
			}
			if policy != "" {
				if err := st.AssignPolicy(ctx, k.User, k.Tenant, policy); err != nil {
					_ = st.RevokeKey(ctx, k.ID)
					return fmt.Errorf("policy %s: %w", policy, err)
				}
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id:     %s\nuser:   %s\ntenant: %s\nscopes: %s\n", k.ID, k.User, k.Tenant, strings.Join(k.Scopes, ","))
			if policy != "" {
				fmt.Fprintf(out, "policy: %s\n", policy)
			}
			if k.Expires != nil {
				fmt.Fprintf(out, "expires: %s\n", k.Expires.Local().Format(time.RFC3339))
			}
			fmt.Fprintf(out, "secret: %s\n", secret)
			if invite != "" {
				// The key rides in the fragment, which a browser never sends
				// to a server: hosted Studio's page reads it and exchanges it
				// for a session (docs/studio.md#hosted-studio).
				fmt.Fprintf(out, "invite: %s/#key=%s\n", invite, secret)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "The secret is shown once and stored only as a hash; keep it now.")
			if invite != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "The invite link is the key: send it as you would a password.")
				if slices.Contains(k.Scopes, gateway.ScopeAdmin) {
					fmt.Fprintln(cmd.ErrOrStderr(), api.AdminInviteNote)
				}
			}
			return nil
		},
	}
	create.Flags().StringVar(&invite, "invite-url", "", "a hosted Studio's address (https://studio.example.com): also print the invite link that signs the user in")
	create.Flags().StringVar(&user, "user", "", "the user the key acts as; required")
	create.Flags().StringVar(&expires, "expires", "", "how long the key works: 7d, 12h, or never (the default)")
	create.Flags().StringVar(&policy, "policy", "", "assign the user this policy (sandbox-gateway policies set) as the key is issued")
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
			ctx := cmd.Context()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tUSER\tTENANT\tSCOPES\tPOLICY\tCREATED\tEXPIRES\tSTATE\tEMAIL")
			now := time.Now()
			keys, err := st.Keys(ctx)
			if err != nil {
				return err
			}
			for _, k := range keys {
				// A sign-in's key is its account's, for a while (accounts.go).
				if k.Parent != "" {
					continue
				}
				state := "active"
				switch {
				case k.Revoked:
					state = "revoked"
				case !k.Active(now):
					state = "expired"
				}
				exp := "never"
				if k.Expires != nil {
					exp = k.Expires.Format(time.RFC3339)
				}
				pol := "-"
				if p, ok, _ := st.PolicyOf(ctx, k.User, k.Tenant); ok {
					pol = p.Name
				}
				email := "-"
				if k.Email != "" {
					email = k.Email
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", k.ID, k.User, k.Tenant, strings.Join(k.Scopes, ","),
					pol, k.Created.Format(time.RFC3339), exp, state, email)
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
			ctx := cmd.Context()
			if err := st.RevokeKey(ctx, args[0]); err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(create, list, revoke)
	return cmd
}

// --- policies -------------------------------------------------------------------

// newPolicies edits the user policies in the state file, as keys does, for
// a gateway that is not serving; a serving one is changed through its admin
// API (sandbox-cli gateway policies).
func newPolicies(state *string) *cobra.Command {
	cmd := &cobra.Command{Use: "policies", Short: "Set, list, assign and remove user policies in the state file"}
	var maxLife time.Duration
	set := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or change a policy; a change holds for sandboxes created after it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxLife%time.Second != 0 {
				return errors.New("--max-lifetime is in whole seconds")
			}
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			p := api.UserPolicy{Name: args[0], MaxLifetimeSecs: int(maxLife / time.Second)}
			if err := st.PutPolicy(ctx, p); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "policy %s: max lifetime %s\n", p.Name, lifetimeText(p.MaxLifetimeSecs))
			return nil
		},
	}
	set.Flags().DurationVar(&maxLife, "max-lifetime", 0, "how long each of a user's sandboxes may exist, busy or not (30m); 0 is no bound of the policy's own")
	list := &cobra.Command{
		Use:   "list",
		Short: "List policies and the users holding each",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			holders := map[string][]string{}
			seen := map[string]bool{}
			keys, err := st.Keys(ctx)
			if err != nil {
				return err
			}
			for _, k := range keys {
				id := k.User + "@" + k.Tenant
				if p, ok, _ := st.PolicyOf(ctx, k.User, k.Tenant); ok && !k.Revoked && !seen[id] {
					seen[id] = true
					holders[p.Name] = append(holders[p.Name], id)
				}
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tMAX LIFETIME\tUSERS")
			pols, err := st.Policies(ctx)
			if err != nil {
				return err
			}
			for _, p := range pols {
				users := strings.Join(holders[p.Name], ",")
				if users == "" {
					users = "-"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, lifetimeText(p.MaxLifetimeSecs), users)
			}
			return tw.Flush()
		},
	}
	var tenant string
	var none bool
	assign := &cobra.Command{
		Use:   "assign USER [POLICY]",
		Short: "Give a user a policy, or none with --none",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			switch {
			case none && len(args) == 2:
				return errors.New("give a policy or --none, not both")
			case !none && len(args) == 1:
				return errors.New("give a policy, or --none to clear the user's")
			case !none:
				name = args[1]
			}
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			if err := st.AssignPolicy(ctx, args[0], tenant, name); err != nil {
				return err
			}
			if name == "" {
				name = "none"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: policy %s\n", args[0], name)
			return nil
		},
	}
	assign.Flags().StringVar(&tenant, "tenant", "", "the tenant of the user's keys")
	assign.Flags().BoolVar(&none, "none", false, "clear the user's policy")
	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a policy no user holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := openState(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			if err := st.DeletePolicy(ctx, args[0]); err != nil {
				return fmt.Errorf("%s: %w", args[0], err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", args[0])
			return nil
		},
	}
	cmd.AddCommand(set, list, assign, remove)
	return cmd
}

// lifetimeText is a lifetime in seconds as a person reads it.
func lifetimeText(secs int) string {
	if secs == 0 {
		return "none"
	}
	return (time.Duration(secs) * time.Second).String()
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
			ctx := cmd.Context()
			if err := st.PutNode(ctx, cfg); err != nil {
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
			ctx := cmd.Context()
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tENDPOINT\tTOKEN FILE\tCA FILE\tCLIENT CERT")
			nodes, err := st.Nodes(ctx)
			if err != nil {
				return err
			}
			for _, n := range nodes {
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
			ctx := cmd.Context()
			if err := st.RemoveNode(ctx, args[0]); err != nil {
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

// mailConfig is how invites are emailed, nil with no --smtp-host. The
// password is read from a file others cannot read, never taken as a flag.
func mailConfig(o serveOptions) (*gateway.MailConfig, error) {
	if o.smtpHost == "" {
		if o.smtpUser != "" || o.smtpPasswordFile != "" || o.mailFrom != "" || o.studioURL != "" {
			return nil, errors.New("--smtp-user, --smtp-password-file, --mail-from and --studio-url go with --smtp-host")
		}
		return nil, nil
	}
	c := &gateway.MailConfig{Host: o.smtpHost, Port: o.smtpPort, User: o.smtpUser, From: o.mailFrom, StudioURL: o.studioURL}
	if o.smtpPasswordFile != "" {
		pw, err := gateway.ReadSecretFile(o.smtpPasswordFile)
		if err != nil {
			return nil, fmt.Errorf("--smtp-password-file: %w", err)
		}
		c.Password = pw
	}
	if err := c.Check(); err != nil {
		return nil, err
	}
	return c, nil
}

// --- store ----------------------------------------------------------------------

// newStore is `sandbox-gateway store`: moving a gateway's state into a
// database, and looking after the database's schema.
func newStore(state *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "store",
		Short: "Import a gateway's state file into its database; apply migrations; show what the database holds",
	}
	var from string
	imp := &cobra.Command{
		Use:   "import",
		Short: "Copy a state file from before databases into the gateway's database, once",
		Long: "Copies every key (with its id and hash, so secrets handed out keep working),\n" +
			"organisation, member, policy, invite, owner, node, sealed secret, job and service\n" +
			"from a state file into the database, in one transaction. The database must hold\n" +
			"no gateway state yet, and the gateway must be stopped (the file is locked while\n" +
			"it runs). The file is left as it was; move it aside once the gateway serves.",
		Example: "  sandbox-gateway --state-dir /var/lib/sandbox-gateway store import --from /var/lib/sandbox-gateway/state.json\n" +
			"  sandbox-gateway --state-dir /var/lib/sandbox-gateway --store postgres --store-dsn-file /etc/sandbox-gateway/db.dsn \\\n" +
			"    store import --from /var/lib/sandbox-gateway/state.json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *state == "" {
				return errors.New("--state-dir is required")
			}
			st, err := openStoreAt(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			c, err := gateway.ImportState(cmd.Context(), from, st)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "imported %s: %s\n", from, c)
			return nil
		},
	}
	imp.Flags().StringVar(&from, "from", "", "the state file to import; required")
	_ = imp.MarkFlagRequired("from")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show the database's schema version and how much it holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *state == "" {
				return errors.New("--state-dir is required")
			}
			st, err := openStoreAt(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := cmd.Context()
			v, err := gateway.SchemaVersion(ctx, st)
			if err != nil {
				return err
			}
			keys, err := st.Keys(ctx)
			if err != nil {
				return err
			}
			orgs, err := st.Orgs(ctx)
			if err != nil {
				return err
			}
			live, err := st.LiveSandboxes(ctx)
			if err != nil {
				return err
			}
			nodes, err := st.Nodes(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "store:     %s\nschema:    %d\nkeys:      %d\norgs:      %d\nsandboxes: %d live\nnodes:     %d\n",
				storeFlags.kindOrDefault(), v, len(keys), len(orgs), len(live), len(nodes))
			return nil
		},
	}
	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Bring the database's schema up to this gateway's (serve does this too)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *state == "" {
				return errors.New("--state-dir is required")
			}
			st, err := openStoreAt(*state)
			if err != nil {
				return err
			}
			defer st.Close()
			v, err := gateway.SchemaVersion(cmd.Context(), st)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "schema at version %d\n", v)
			return nil
		},
	}
	cmd.AddCommand(imp, status, migrate)
	return cmd
}
