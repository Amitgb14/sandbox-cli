package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// SSH through a gateway. A gateway in front of many nodes runs one SSH
// server for every sandbox: the SSH user name is the sandbox, and the login
// is a public key the user registered, or a short-lived token. `sandbox-cli
// ssh` does the bookkeeping that makes the system ssh client work against it
// — registers the key, pins the gateway's host key — and then hands the
// terminal to ssh. A plain sandboxd has no SSH server; there the same command
// connects through the API, as `run` and `attach` already do.
//
// Nothing here parses or holds a private key. The CLI reads only the public
// half, and ssh itself does the authentication, so the dependency rule (no
// SSH library in the CLI) and the secrets rule (a private key never passes
// through our process) are both kept by construction.

// pubKey is one OpenSSH public key: its type, its wire-format blob, and the
// comment that followed it.
type pubKey struct {
	Type    string
	Blob    []byte
	Comment string
}

// sshKeyTypes are the public key types ssh-keygen makes. A line starting with
// anything else is either not a key or carries authorized_keys options
// (command=, from=), which the gateway refuses and we refuse first.
var sshKeyTypes = map[string]bool{
	"ssh-ed25519":                        true,
	"ssh-rsa":                            true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp521":                true,
	"sk-ssh-ed25519@openssh.com":         true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
}

// parsePublicKey parses "type base64 [comment]". The blob must name the same
// type as the line does: a mismatch is a corrupt or doctored key. Errors never
// quote the line — it may be a private key passed by mistake.
func parsePublicKey(line string) (pubKey, error) {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "-----BEGIN") {
		return pubKey{}, errors.New("this is a private key; give the public half (the .pub file)")
	}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return pubKey{}, errors.New("not an OpenSSH public key (want: type base64 [comment])")
	}
	if !sshKeyTypes[fields[0]] {
		return pubKey{}, errors.New("not an OpenSSH public key, or one with authorized_keys options (which are refused)")
	}
	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return pubKey{}, errors.New("the key's base64 does not decode")
	}
	if len(blob) < 4 {
		return pubKey{}, errors.New("the key is truncated")
	}
	n := binary.BigEndian.Uint32(blob)
	if uint64(n) > uint64(len(blob)-4) || string(blob[4:4+n]) != fields[0] {
		return pubKey{}, errors.New("the key's contents do not match its type")
	}
	return pubKey{Type: fields[0], Blob: blob, Comment: strings.Join(fields[2:], " ")}, nil
}

// Fingerprint is the key's SHA256 fingerprint as ssh-keygen -l prints it.
func (k pubKey) Fingerprint() string {
	sum := sha256.Sum256(k.Blob)
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

// Line is the key as one authorized_keys line, without options.
func (k pubKey) Line() string {
	s := k.Type + " " + base64.StdEncoding.EncodeToString(k.Blob)
	if k.Comment != "" {
		s += " " + k.Comment
	}
	return s
}

// readPublicKey reads the first key in a .pub file.
func readPublicKey(path string) (pubKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return pubKey{}, err
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, 64<<10))
	sc.Buffer(make([]byte, 0, 16<<10), 64<<10)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, err := parsePublicKey(line)
		if err != nil {
			return pubKey{}, fmt.Errorf("%s: %w", path, err)
		}
		return k, nil
	}
	if err := sc.Err(); err != nil {
		return pubKey{}, fmt.Errorf("%s: %w", path, err)
	}
	return pubKey{}, fmt.Errorf("%s: no public key in it", path)
}

// sshIdentity is the key a login uses: the public half we register, and what
// ssh is told to offer with -i.
type sshIdentity struct {
	Pub string // the .pub file
	// Offer is the private half when it exists, else the .pub itself, which
	// tells ssh to use the matching key from the agent (a hardware key, or
	// one whose private file lives elsewhere).
	Offer string
}

// defaultIdentities are tried in order when --identity is not given: the
// ones ssh-keygen makes, newest kind first.
var defaultIdentities = []string{"id_ed25519.pub", "id_ecdsa.pub", "id_rsa.pub"}

// resolveIdentity turns --identity (a .pub file, or a private key whose .pub
// sits beside it) into an sshIdentity; empty picks the first default that
// exists under sshDir.
func resolveIdentity(flag, sshDir string) (sshIdentity, error) {
	pub := ""
	switch {
	case flag == "":
		for _, name := range defaultIdentities {
			p := filepath.Join(sshDir, name)
			if _, err := os.Stat(p); err == nil {
				pub = p
				break
			}
		}
		if pub == "" {
			return sshIdentity{}, fmt.Errorf("no public key in %s (%s); make one with `ssh-keygen -t ed25519`, or give --identity",
				sshDir, strings.Join(defaultIdentities, ", "))
		}
	case strings.HasSuffix(flag, ".pub"):
		pub = flag
	default:
		pub = flag + ".pub"
	}
	priv := strings.TrimSuffix(pub, ".pub")
	if _, err := os.Stat(priv); err == nil {
		return sshIdentity{Pub: pub, Offer: priv}, nil
	}
	return sshIdentity{Pub: pub, Offer: pub}, nil
}

// knownHostsHost is the host field ssh looks up: the bare host on port 22,
// [host]:port otherwise.
func knownHostsHost(host string, port int) string {
	if port == 22 {
		return host
	}
	return "[" + host + "]:" + strconv.Itoa(port)
}

// knownHostsLine is one pinned host key.
func knownHostsLine(host string, port int, k pubKey) string {
	return knownHostsHost(host, port) + " " + k.Type + " " + base64.StdEncoding.EncodeToString(k.Blob)
}

// sshHostRE is a host name or address as the gateway may report it. It is
// written into known_hosts and into ssh's argv, so it allows nothing either
// would read as syntax: no spaces, commas, wildcards, '!' or '@', and no
// leading '-'.
var sshHostRE = regexp.MustCompile(`^[A-Za-z0-9_.:][A-Za-z0-9_.:-]*$`)

// pinHostKeys replaces the entries for host:port in the known_hosts file at
// path with keys, keeping every other line. The keys come from the gateway
// (GET /v1/ssh), reached over the context's authenticated, verified HTTPS: it
// is the same trust that already decides what the API answers.
func pinHostKeys(path, host string, port int, keys []pubKey) error {
	field := knownHostsHost(host, port)
	var kept []string
	if old, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(old), "\n") {
			if line == "" {
				continue
			}
			if f := strings.Fields(line); len(f) > 0 && f[0] == field {
				continue
			}
			kept = append(kept, line)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, k := range keys {
		kept = append(kept, knownHostsLine(host, port, k))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func knownHostsPath() string { return filepath.Join(agenthome.ConfigDir(), "known_hosts") }

// sshTarget is where ssh connects and what it trusts there.
type sshTarget struct {
	Host string
	Port int
	Keys []pubKey
}

// gatewaySSHTarget checks what GET /v1/ssh said. A host it does not give is
// the API endpoint's own; a host key that does not parse refuses the login,
// since connecting without a pinned key is exactly what StrictHostKeyChecking
// is there to prevent.
func gatewaySSHTarget(info api.SSHInfo, endpointHost string) (sshTarget, error) {
	t := sshTarget{Host: info.Host, Port: info.Port}
	if t.Host == "" {
		t.Host = endpointHost
	}
	if !sshHostRE.MatchString(t.Host) {
		return t, fmt.Errorf("the gateway reports SSH host %q, which is not a host name", termsafe.Clean(t.Host))
	}
	if t.Port <= 0 || t.Port > 65535 {
		return t, fmt.Errorf("the gateway reports SSH port %d", t.Port)
	}
	for _, line := range info.HostKeys {
		k, err := parsePublicKey(line)
		if err != nil {
			return t, fmt.Errorf("the gateway's host key: %w", err)
		}
		t.Keys = append(t.Keys, k)
	}
	if len(t.Keys) == 0 {
		return t, errors.New("the gateway publishes no SSH host key to pin; refusing to connect without one")
	}
	return t, nil
}

// sshArgv is the ssh command line (without "ssh" itself) for user at t,
// trusting only the keys pinned in knownHosts and offering only identity.
// "--" ends ssh's options, so neither the user nor the command can be read
// as one.
func sshArgv(t sshTarget, user, knownHosts, identity string, command []string) []string {
	argv := []string{
		"-p", strconv.Itoa(t.Port),
		"-o", "UserKnownHostsFile=" + knownHosts,
		// Only the key we pinned vouches for the gateway, not a system-wide
		// entry for the same name.
		"-o", "GlobalKnownHostsFile=" + os.DevNull,
		"-o", "StrictHostKeyChecking=yes",
	}
	if identity != "" {
		// The key we registered is the one offered: an agent holding many
		// keys otherwise spends the server's MaxAuthTries on the others.
		argv = append(argv, "-i", identity, "-o", "IdentitiesOnly=yes")
	}
	argv = append(argv, "--", user+"@"+t.Host)
	return append(argv, command...)
}

// validSSHUser reports whether ref may be put in ssh's argv as a user name:
// a sandbox id or name, which also keeps it free of '@', spaces and a
// leading '-'.
func validSSHUser(ref string) bool { return spec.ValidID(ref) || spec.ValidName(ref) }

// ensureSSHKey registers k with the gateway unless it already is, for every
// sandbox or for ref. It reports whether it registered it.
func ensureSSHKey(ctx context.Context, c *api.Client, k pubKey, ref string) (bool, error) {
	keys, err := c.SSHKeys(ctx)
	if err != nil {
		return false, err
	}
	fp := k.Fingerprint()
	for _, have := range keys {
		if have.Fingerprint == fp && (have.Sandbox == "" || have.Sandbox == ref) {
			return false, nil
		}
	}
	if _, err := c.AddSSHKey(ctx, k.Line(), ""); err != nil {
		return false, fmt.Errorf("registering %s: %w", fp, err)
	}
	return true, nil
}

// sshRoute is how `sandbox-cli ssh` reaches a sandbox on this endpoint.
type sshRoute int

const (
	routeSSH sshRoute = iota // a gateway: its SSH server
	routeAPI                 // a plain sandboxd: a process over the API
)

// chooseSSHRoute asks the endpoint what it is. Only a clear "no such
// endpoint" means a plain sandboxd; a refused key or an unreachable gateway
// is an error, not a reason to try something else.
func chooseSSHRoute(ctx context.Context, c *api.Client) (sshRoute, error) {
	gw, err := c.IsGateway(ctx)
	if err != nil {
		return routeSSH, err
	}
	if gw {
		return routeSSH, nil
	}
	return routeAPI, nil
}

// endpointHost is the host name of the client's endpoint, the SSH host when
// the gateway does not name one.
func endpointHost(c *api.Client) string {
	base, _, _ := c.Transport()
	if base == nil {
		return ""
	}
	return base.Hostname()
}

// notGateway is the refusal for a gateway-only command on a plain sandboxd.
func notGateway(ctxName, what string) error {
	return fmt.Errorf("context %s is a plain sandboxd, which has no users and no SSH server: %s needs a gateway (sandbox-cli context add NAME https://gateway --token-file KEYFILE)",
		termsafe.Clean(ctxName), what)
}

// requireGateway connects to the selected context and refuses unless it is a
// gateway.
func requireGateway(ctx context.Context, ctxFlag, what string) (*api.Client, error) {
	c, name, err := newClient(ctxFlag)
	if err != nil {
		return nil, err
	}
	gw, err := c.IsGateway(ctx)
	if err != nil {
		return nil, err
	}
	if !gw {
		return nil, notGateway(name, what)
	}
	return c, nil
}

func newSSHCmd() *cobra.Command {
	var ctxFlag, identity string
	cmd := &cobra.Command{
		Use:   "ssh [flags] SANDBOX [-- COMMAND [ARGS...]]",
		Short: "Open an SSH session to a sandbox through a gateway (or a shell over the API on a plain sandboxd)",
		Long: "Against a gateway, registers your public key if it is not already, pins the\n" +
			"gateway's SSH host key in ~/.config/sandbox/known_hosts, and runs your\n" +
			"ssh client: `ssh -p PORT SANDBOX@HOST`. Afterwards plain ssh works too. With\n" +
			"COMMAND, runs it instead of a login shell. The exit status is ssh's.\n" +
			"Flags go before SANDBOX: everything after it is the command's.\n\n" +
			"The key is --identity (a .pub file, or a private key with its .pub beside\n" +
			"it), else the first of ~/.ssh/id_ed25519.pub, id_ecdsa.pub, id_rsa.pub.\n" +
			"Only the public half is read; ssh does the authentication.\n\n" +
			"A plain sandboxd has no SSH server: there this opens a shell (or runs\n" +
			"COMMAND) in the sandbox through the API instead, and says so.",
		Example: "  sandbox-cli ssh demo\n" +
			"  sandbox-cli ssh demo -- uname -a\n" +
			"  sandbox-cli ssh --identity ~/.ssh/work_ed25519 --context fleet sbx_n1_0123456789abcdef",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, command := args[0], args[1:]
			// With flags not interspersed, a "--" after SANDBOX reaches us
			// as an argument; it only separates.
			if len(command) > 0 && command[0] == "--" {
				command = command[1:]
			}
			ctx := cmd.Context()
			c, name, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			route, err := chooseSSHRoute(ctx, c)
			if err != nil {
				return err
			}
			if route == routeAPI {
				fmt.Fprintf(os.Stderr, "sandbox-cli: context %s is a plain sandboxd with no SSH server; connecting through the API instead\n",
					termsafe.Clean(name))
				argv := command
				if len(argv) == 0 {
					argv = apiShellArgv
				}
				return apiSession(ctx, c, ref, argv)
			}
			return sshThroughGateway(ctx, c, ref, identity, command)
		},
	}
	// Everything after SANDBOX is the command's, so `ssh demo ls -la` does
	// not read -la as ours.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
	cmd.Flags().StringVarP(&identity, "identity", "i", "", "public key file to log in with (or its private half)")
	return cmd
}

func sshThroughGateway(ctx context.Context, c *api.Client, ref, identityFlag string, command []string) error {
	if !validSSHUser(ref) {
		return fmt.Errorf("%q is not a sandbox id or name", termsafe.Clean(ref))
	}
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("no ssh client on PATH: install OpenSSH's client, or use `sandbox-cli attach`")
	}
	home, _ := os.UserHomeDir()
	id, err := resolveIdentity(identityFlag, filepath.Join(home, ".ssh"))
	if err != nil {
		return err
	}
	key, err := readPublicKey(id.Pub)
	if err != nil {
		return err
	}
	added, err := ensureSSHKey(ctx, c, key, ref)
	if err != nil {
		return err
	}
	if added {
		fmt.Fprintf(os.Stderr, "sandbox-cli: registered %s (%s) for SSH logins\n", id.Pub, key.Fingerprint())
	}
	info, err := c.SSHInfo(ctx)
	if err != nil {
		return err
	}
	t, err := gatewaySSHTarget(info, endpointHost(c))
	if err != nil {
		return err
	}
	kh := knownHostsPath()
	if err := pinHostKeys(kh, t.Host, t.Port, t.Keys); err != nil {
		return fmt.Errorf("pinning the gateway's host key: %w", err)
	}
	ssh := exec.CommandContext(ctx, sshPath, sshArgv(t, ref, kh, id.Offer, command)...)
	ssh.Stdin, ssh.Stdout, ssh.Stderr = os.Stdin, os.Stdout, os.Stderr
	// An interrupt is ssh's to handle (it forwards it, or ends the session);
	// we only wait for it.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	err = ssh.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code > 0 {
			return exitError{code}
		}
		return exitError{255}
	}
	return err
}

// apiShellArgv is the guest's best interactive shell: bash when the image has
// it, sh otherwise, as a login shell so the image's profile applies.
var apiShellArgv = []string{"/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}

// apiSession starts argv in a running sandbox and attaches this terminal to
// it until it exits — the plain-sandboxd side of `sandbox-cli ssh`. The
// process is the caller's, so Ctrl-C without a terminal is forwarded to it;
// the sandbox carries on either way.
func apiSession(ctx context.Context, c *api.Client, ref string, argv []string) error {
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
	req := api.RunRequest{Argv: argv, Cwd: agenthome.GuestHome, Tty: tty, Rows: rows, Cols: cols}
	if tty {
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

func newSSHKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-key",
		Short: "Manage the public keys a gateway accepts for SSH logins",
		Long: "A gateway's SSH server logs you in with a public key you registered:\n" +
			"`ssh SANDBOX@gateway -p PORT`. These commands add, list and remove yours.\n" +
			"A plain sandboxd has no SSH server and refuses them.",
	}
	var ctxFlag, sandbox string
	add := &cobra.Command{
		Use:   "add [FILE]",
		Short: "Register a public key (default: ~/.ssh/id_ed25519.pub, id_ecdsa.pub or id_rsa.pub)",
		Example: "  sandbox-cli ssh-key add\n" +
			"  sandbox-cli ssh-key add ~/.ssh/work_ed25519.pub --sandbox demo",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flag := ""
			if len(args) == 1 {
				flag = args[0]
			}
			home, _ := os.UserHomeDir()
			id, err := resolveIdentity(flag, filepath.Join(home, ".ssh"))
			if err != nil {
				return err
			}
			key, err := readPublicKey(id.Pub)
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "ssh-key")
			if err != nil {
				return err
			}
			info, err := c.AddSSHKey(cmd.Context(), key.Line(), sandbox)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", termsafe.Clean(info.ID), termsafe.Clean(info.Fingerprint))
			return nil
		},
	}
	add.Flags().StringVar(&sandbox, "sandbox", "", "accept the key for this sandbox only (default: all of yours)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List your registered keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "ssh-key")
			if err != nil {
				return err
			}
			keys, err := c.SSHKeys(cmd.Context())
			if err != nil {
				return err
			}
			w := newTable(cmd.OutOrStdout())
			fmt.Fprintln(w, "ID\tFINGERPRINT\tSANDBOX\tCOMMENT\tCREATED")
			for _, k := range keys {
				sb := k.Sandbox
				if sb == "" {
					sb = "(all)"
				}
				comment := ""
				if pk, err := parsePublicKey(k.Key); err == nil {
					comment = pk.Comment
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(k.ID), termsafe.Clean(k.Fingerprint),
					termsafe.Clean(sb), termsafe.Clean(comment), k.Created.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	rm := &cobra.Command{
		Use:   "rm ID",
		Short: "Remove a registered key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "ssh-key")
			if err != nil {
				return err
			}
			return c.RemoveSSHKey(cmd.Context(), args[0])
		},
	}
	for _, sub := range []*cobra.Command{add, list, rm} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
		cmd.AddCommand(sub)
	}
	return cmd
}

func newSSHAccessCmd() *cobra.Command {
	var ctxFlag string
	var ttl durationFlag
	cmd := &cobra.Command{
		Use:   "ssh-access SANDBOX",
		Short: "Print a short-lived ssh command for one sandbox, needing no registered key",
		Long: "Asks the gateway for a token that logs in to SANDBOX until it expires, and\n" +
			"prints the ssh command that uses it. The token is the SSH user name and the\n" +
			"whole credential: anyone holding the line can log in until it expires, so\n" +
			"hand it only to whoever should have that access. Gateway only.",
		Example: "  sandbox-cli ssh-access demo\n  sandbox-cli ssh-access demo --ttl 5m",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "ssh-access")
			if err != nil {
				return err
			}
			a, err := c.SSHAccess(cmd.Context(), args[0], ttl.secs)
			if err != nil {
				return err
			}
			line := a.Command
			if line == "" {
				line = fmt.Sprintf("ssh -p %d %s@%s", a.Port, a.User, a.Host)
			}
			fmt.Fprintln(cmd.OutOrStdout(), termsafe.Clean(line))
			fmt.Fprintf(os.Stderr, "sandbox-cli: valid until %s; the user name is the credential\n",
				a.ExpiresAt.Local().Format("2006-01-02 15:04:05 MST"))
			return nil
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
	cmd.Flags().Var(&ttl, "ttl", "how long the token is valid, e.g. 15m (default: the gateway's)")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	var ctxFlag string
	return withContextFlag(&cobra.Command{
		Use:   "whoami",
		Short: "Show who the current context's credential is: user, tenant, scopes and key id on a gateway",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, name, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			w, err := c.Whoami(cmd.Context())
			if api.IsCode(err, api.CodeNotFound) {
				fmt.Fprintf(cmd.OutOrStdout(), "context %s is a single-tenant sandboxd: it has no users, and its token is its operator's\n",
					termsafe.Clean(name))
				return nil
			}
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintf(t, "user\t%s\n", termsafe.Clean(w.User))
			fmt.Fprintf(t, "tenant\t%s\n", termsafe.Clean(w.Tenant))
			fmt.Fprintf(t, "key\t%s\n", termsafe.Clean(w.KeyID))
			fmt.Fprintf(t, "scopes\t%s\n", termsafe.Clean(strings.Join(w.Scopes, ", ")))
			return t.Flush()
		},
	}, &ctxFlag)
}

func withContextFlag(cmd *cobra.Command, p *string) *cobra.Command {
	cmd.Flags().StringVar(p, "context", "", "which endpoint to use")
	return cmd
}

// durationFlag is a positive duration given in whole seconds to the API.
type durationFlag struct{ secs int }

func (d *durationFlag) String() string {
	if d.secs == 0 {
		return ""
	}
	return strconv.Itoa(d.secs) + "s"
}

func (d *durationFlag) Set(s string) error {
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	if v.Seconds() < 1 {
		return errors.New("must be at least 1s")
	}
	d.secs = int(v.Seconds())
	return nil
}

func (d *durationFlag) Type() string { return "duration" }

func newTable(w io.Writer) *tabwriter.Writer { return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) }
