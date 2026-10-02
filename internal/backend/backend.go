// Package backend is the seam between what a sandbox should be and the thing
// that makes one: a VM under the macOS `container` runtime, a Firecracker
// microVM, or the in-memory fake.
//
// The old tree had a Runtime interface, but it was docker-shaped and twelve
// files named the concrete docker client anyway. Here the server holds a
// Backend and nothing else. A backend does not decide policy — spec has already
// resolved the request against the server's limits and network ceiling — and it
// does not keep the sandbox list; the server does, so that a reference is only
// ever matched against sandboxes this server created.
//
// What a backend must do is report honestly what it can honour, through
// Capabilities, so that a request needing more is refused before it reaches the
// backend rather than quietly weakened inside it.
package backend

import (
	"context"
	"errors"
	"io"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Spec is a fully resolved sandbox: every default applied, every limit checked,
// every policy decision made. A backend renders it; it never second-guesses it.
type Spec struct {
	ID       string
	Image    string
	CPUs     float64
	MemoryMB int
	DiskMB   int
	Env      map[string]string
	Network  api.NetworkPolicy
	// IdleTimeoutSecs is enforced by the server, not the backend; carried here
	// so the resolved spec is the whole decision.
	IdleTimeoutSecs int
	// Bind, when set, is a host directory — already resolved and checked by
	// hostpath — to mount at /workspace.
	Bind *Bind
}

// Bind is a host directory mounted at /workspace.
type Bind struct {
	HostPath string // absolute, symlinks resolved, refused if /, home or an ancestor
	ReadOnly bool
}

// ProcSpec is one process to start inside a sandbox. Env is merged over the
// sandbox's own by the server before it gets here.
type ProcSpec struct {
	Argv []string
	Env  map[string]string
	Cwd  string
}

// Proc is a started process.
type Proc interface {
	// Stdin is the process's standard input. Closing it delivers EOF.
	Stdin() io.WriteCloser
	// Signal delivers one of api.Signals.
	Signal(sig string) error
	// Wait blocks until the process exits and returns its exit code — 128+n for
	// death by signal n, as a shell reports it — once every write to the stdout
	// and stderr writers passed to Start has returned.
	Wait() int
}

// Backend makes and runs sandboxes.
type Backend interface {
	// Name identifies the backend in /v1/capabilities ("fake", "macos",
	// "firecracker").
	Name() string
	// Capabilities reports which api.Cap* this backend honours. Absent means no.
	Capabilities() map[string]bool

	// Create makes the sandbox and returns once it is running.
	Create(ctx context.Context, s Spec) error
	// UpdateNetwork replaces a running sandbox's egress policy, atomically: there
	// is no moment where neither the old nor the new policy is in force. Only
	// called when Capabilities includes api.CapNetworkPolicyUpdate.
	UpdateNetwork(ctx context.Context, id string, p api.NetworkPolicy) error
	// Terminate stops every process and discards the sandbox. Terminating an
	// unknown or already-terminated id is not an error.
	Terminate(ctx context.Context, id string) error

	// Start runs a process, copying its output to stdout and stderr as it is
	// produced. Start returns as soon as the process is running.
	Start(ctx context.Context, id string, p ProcSpec, stdout, stderr io.Writer) (Proc, error)

	// File operations take absolute guest paths, already validated by the server.
	ReadFile(ctx context.Context, id, path string) ([]byte, error)
	WriteFile(ctx context.Context, id, path string, data []byte) error
	Remove(ctx context.Context, id, path string) error
	ListDir(ctx context.Context, id, path string) ([]api.DirEntry, error)
}

// Errors a backend returns for the server to map onto API codes. Anything else
// is reported as internal.
var (
	ErrNotFound    = errors.New("not found")
	ErrIsDir       = errors.New("is a directory")
	ErrNotDir      = errors.New("not a directory")
	ErrNotEmpty    = errors.New("directory not empty")
	ErrNoSuchCmd   = errors.New("no such command")
	ErrBadSignal   = errors.New("unsupported signal")
	ErrUnavailable = errors.New("backend unavailable")
)
