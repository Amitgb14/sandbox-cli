// Package macos is the local backend: on a Mac (macOS 26+, arm64) each sandbox
// is a lightweight VM run by the native `container` runtime — its own kernel,
// one per sandbox.
//
// Everything here is the `container` CLI driven through os/exec, and the
// argument list is a pure function (BuildRunArgs) with a golden test. The guest
// agent is the same sandbox-guestd as on Linux: it is mounted read-only from
// the host into every sandbox, so any image works and the agent always matches
// sandboxd, and each request is one `container exec -i … serve --stdio`.
//
// What the runtime can and cannot do decides the capabilities, and two of them
// wait on measurement (rewrite M3, scripts/m3/macos):
//   - egress: the runtime's default network is open NAT. Whether an allowlist
//     can be enforced — in the guest, or outside it — is not known yet, so this
//     backend offers none and open only, and the server's policy follows.
//   - ownership of bind-mounted files: how the runtime maps uids on its shared
//     directories decides whether the host can edit what the agent wrote.
//
// Untested on macOS itself until the maintainer runs it; on Linux the driver
// runs against a fake `container` that executes the real guest agent (see
// macos_test.go), which covers everything but the runtime.
package macos

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// AgentDir is where the host's guest agent is mounted in every sandbox.
const AgentDir = "/.sbx"

// LabelManaged marks containers this backend created, so a restart can find and
// remove what an earlier sandboxd left behind — and touch nothing else.
const LabelManaged = "sbx.managed"

// Config is how this Mac runs sandboxes.
type Config struct {
	Container string // the `container` CLI
	// Agent is sandbox-guestd built for linux/arm64. Its directory is mounted
	// read-only into every sandbox.
	Agent string
	Logf  func(format string, a ...any)
	// BootTimeout bounds how long a create waits for the guest agent.
	BootTimeout time.Duration
}

// Backend runs sandboxes with the `container` runtime.
type Backend struct {
	cfg Config
	mu  sync.Mutex
	ids map[string]bool
}

// New checks the CLI is there and removes sandboxes an earlier sandboxd left.
func New(cfg Config) (*Backend, error) {
	bin, err := exec.LookPath(cfg.Container)
	if err != nil {
		return nil, fmt.Errorf("macos backend: the container CLI: %w", err)
	}
	cfg.Container = bin
	if _, err := os.Stat(cfg.Agent); err != nil {
		return nil, fmt.Errorf("macos backend: guest agent: %w", err)
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.BootTimeout == 0 {
		cfg.BootTimeout = 60 * time.Second
	}
	b := &Backend{cfg: cfg, ids: map[string]bool{}}
	b.reapLeftovers()
	return b, nil
}

func (b *Backend) Name() string { return "macos" }

func (b *Backend) Capabilities() map[string]bool {
	return map[string]bool{
		api.CapEgressOpen:      true,
		api.CapBindWorkspace:   true,
		api.CapWorkspaceBundle: true,
	}
}

// BuildRunArgs is the `container run` argument list for a sandbox: a pure
// function of the resolved spec and the host's agent directory. The agent is
// mounted read-only; a bind, when asked for, is the only other host path, and
// it has already passed hostpath's refusals.
func BuildRunArgs(s backend.Spec, agentDir string) ([]string, error) {
	if s.Network.Mode == api.NetworkAllowlist {
		return nil, errors.New("the macos backend cannot enforce an egress allowlist")
	}
	cpus := int(s.CPUs + 0.999)
	if cpus < 1 {
		cpus = 1
	}
	args := []string{
		"run", "--detach",
		"--name", s.ID,
		"--label", LabelManaged + "=1",
		"--cpus", fmt.Sprint(cpus),
		"--memory", fmt.Sprintf("%dM", s.MemoryMB),
		"--mount", "type=bind,source=" + agentDir + ",target=" + AgentDir + ",readonly",
	}
	if s.Network.Mode == api.NetworkNone {
		args = append(args, "--network", "none")
	}
	if s.Bind != nil {
		if strings.ContainsRune(s.Bind.HostPath, ',') {
			return nil, fmt.Errorf("bind path %q contains a comma, which the mount syntax cannot express", s.Bind.HostPath)
		}
		m := "type=bind,source=" + s.Bind.HostPath + ",target=/workspace"
		if s.Bind.ReadOnly {
			m += ",readonly"
		}
		args = append(args, "--mount", m)
	}
	// "--" so an image reference can never be read as a flag.
	return append(args, "--", s.Image, AgentDir+"/sandbox-guestd", "idle"), nil
}

// BuildExecArgs is the `container exec` argument list for one request.
func BuildExecArgs(id string) []string {
	return []string{"exec", "--interactive", id, AgentDir + "/sandbox-guestd", "serve", "--stdio"}
}

func (b *Backend) cli(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, b.cfg.Container, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("container %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (b *Backend) reapLeftovers() {
	out, err := b.cli(context.Background(), "ls", "--all", "--format", "json")
	if err != nil {
		return
	}
	var list []struct {
		Configuration struct {
			ID     string            `json:"id"`
			Labels map[string]string `json:"labels"`
		} `json:"configuration"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return
	}
	for _, c := range list {
		if c.Configuration.Labels[LabelManaged] == "1" {
			_, _ = b.cli(context.Background(), "rm", "--force", c.Configuration.ID)
			b.cfg.Logf("removed a sandbox left by an earlier run: %s", c.Configuration.ID)
		}
	}
}

func (b *Backend) client(id string) *guestproto.Client {
	return &guestproto.Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		return dialExec(ctx, b.cfg.Container, BuildExecArgs(id))
	}}
}

func (b *Backend) known(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.ids[id] {
		return backend.ErrNotFound
	}
	return nil
}

// Create starts the sandbox and waits for the guest agent to answer.
func (b *Backend) Create(ctx context.Context, s backend.Spec) error {
	args, err := BuildRunArgs(s, filepath.Dir(b.cfg.Agent))
	if err != nil {
		return err
	}
	t0 := time.Now()
	if _, err := b.cli(ctx, args...); err != nil {
		b.cfg.Logf("sandbox %s: %v", s.ID, err)
		return errors.New("the runtime could not start the sandbox")
	}
	rctx, cancel := context.WithTimeout(ctx, b.cfg.BootTimeout)
	defer cancel()
	if err := b.client(s.ID).WaitReady(rctx); err != nil {
		_, _ = b.cli(context.Background(), "rm", "--force", s.ID)
		return fmt.Errorf("the guest did not come up: %w", err)
	}
	b.cfg.Logf("sandbox %s: ready in %v", s.ID, time.Since(t0).Round(time.Millisecond))
	b.mu.Lock()
	b.ids[s.ID] = true
	b.mu.Unlock()
	return nil
}

func (b *Backend) UpdateNetwork(context.Context, string, api.NetworkPolicy) error {
	return errors.New("the macos backend cannot change a running sandbox's network")
}

func (b *Backend) Terminate(ctx context.Context, id string) error {
	b.mu.Lock()
	known := b.ids[id]
	delete(b.ids, id)
	b.mu.Unlock()
	if !known {
		return nil
	}
	_, err := b.cli(ctx, "rm", "--force", id)
	return err
}

// Close removes every sandbox this backend started.
func (b *Backend) Close() {
	b.mu.Lock()
	ids := make([]string, 0, len(b.ids))
	for id := range b.ids {
		ids = append(ids, id)
	}
	b.mu.Unlock()
	for _, id := range ids {
		_ = b.Terminate(context.Background(), id)
	}
}

func (b *Backend) Start(ctx context.Context, id string, p backend.ProcSpec, stdout, stderr io.Writer) (backend.Proc, error) {
	if err := b.known(id); err != nil {
		return nil, err
	}
	proc, err := b.client(id).ExecRequest(ctx, guestproto.Request{Argv: p.Argv, Env: p.Env, Cwd: p.Cwd, Tty: p.Tty, Rows: p.Rows, Cols: p.Cols}, stdout, stderr)
	if err != nil {
		return nil, mapErr(err)
	}
	return proc, nil
}

func (b *Backend) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	if err := b.known(id); err != nil {
		return nil, err
	}
	data, err := b.client(id).ReadFile(ctx, path)
	return data, mapErr(err)
}

func (b *Backend) WriteFile(ctx context.Context, id, path string, data []byte) error {
	if err := b.known(id); err != nil {
		return err
	}
	return mapErr(b.client(id).WriteFile(ctx, path, data))
}

func (b *Backend) Remove(ctx context.Context, id, path string) error {
	if err := b.known(id); err != nil {
		return err
	}
	return mapErr(b.client(id).Remove(ctx, path))
}

func (b *Backend) ListDir(ctx context.Context, id, path string) ([]api.DirEntry, error) {
	if err := b.known(id); err != nil {
		return nil, err
	}
	entries, err := b.client(id).List(ctx, path)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]api.DirEntry, 0, len(entries))
	for _, e := range entries {
		t := e.Type
		if t != "dir" {
			t = "file"
		}
		out = append(out, api.DirEntry{Name: e.Name, Type: t, Size: e.Size})
	}
	return out, nil
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var ge *guestproto.Error
	if !errors.As(err, &ge) {
		return err
	}
	switch ge.Code {
	case guestproto.CodeNotFound:
		return backend.ErrNotFound
	case guestproto.CodeIsDir:
		return backend.ErrIsDir
	case guestproto.CodeNotDir:
		return backend.ErrNotDir
	case guestproto.CodeNotEmpty:
		return backend.ErrNotEmpty
	case guestproto.CodeNoSuchCommand:
		return backend.ErrNoSuchCmd
	}
	return err
}

// dialExec runs one `container exec -i` and presents its stdio as a connection.
// Closing it ends the exec, which is what tells the guest agent the host has
// gone — and the agent then kills the process it was running.
func dialExec(ctx context.Context, bin string, args []string) (io.ReadWriteCloser, error) {
	cmd := exec.Command(bin, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &execConn{cmd: cmd, w: stdin, r: bufio.NewReaderSize(stdout, 64<<10), done: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(c.done) }()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	c.stop = stop
	return c, nil
}

type execConn struct {
	cmd  *exec.Cmd
	w    io.WriteCloser
	r    *bufio.Reader
	done chan struct{}
	stop func() bool
	once sync.Once
}

func (c *execConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *execConn) Write(p []byte) (int, error) { return c.w.Write(p) }

func (c *execConn) Close() error {
	c.once.Do(func() {
		if c.stop != nil {
			c.stop()
		}
		_ = c.w.Close()
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
			_ = c.cmd.Process.Kill()
			<-c.done
		}
	})
	return nil
}
