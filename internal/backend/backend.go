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
	"time"

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
	// SnapshotEverySecs and SnapshotKeep are the snapshot schedule, kept by
	// the server like the idle timeout; carried for the same reason.
	SnapshotEverySecs int
	SnapshotKeep      int
	// FromSnapshot, when set, starts the sandbox from a snapshot of another —
	// memory, processes and disk as they were — instead of booting the image.
	FromSnapshot string
	// Volumes are mounted at their paths. The server has checked that each
	// exists, is attached nowhere else, and that the paths are allowed.
	Volumes []api.VolumeMount
}

// Keeper can leave its sandboxes running when sandboxd exits, and take back,
// when the next sandboxd starts on the same state directory, the ones an
// earlier one left. That is how sandboxd is upgraded or restarted without
// interrupting the VMs it serves: their disks, memory and network carry on.
// Their processes do not: the guest agent ends a process whose host
// connection goes, so a restart ends every running command.
//
// A backend takes back only what it can prove is its own and unchanged: a VM
// whose record it wrote, whose VMM is the same live process, and whose guest
// agent answers. Anything else it finds is removed, as a backend that keeps
// nothing removes everything.
type Keeper interface {
	// Kept lists the sandboxes this backend took back when it started, by id.
	// The server pairs them with its own records and terminates any it has
	// none for, so a VM is never served without the record that says whose
	// it is.
	Kept() map[string]Kept
	// Detach lets go of every sandbox without stopping it: sandboxd is about
	// to exit and a later one will take them back. Nothing may be called
	// after it.
	Detach()
}

// Kept is one sandbox a Keeper took back.
type Kept struct {
	Suspended bool
}

// VolumeStore keeps named volumes: filesystems that outlive the sandboxes they
// are mounted in. The backend's own storage is the record of what exists, so
// volumes survive a restart of sandboxd, which keeps no sandbox records.
type VolumeStore interface {
	CreateVolume(ctx context.Context, name string, sizeMB int) error
	DeleteVolume(ctx context.Context, name string) error
	Volumes(ctx context.Context) ([]VolumeInfo, error)
}

// VolumeInfo is one stored volume.
type VolumeInfo struct {
	Name      string
	SizeMB    int
	CreatedAt time.Time
}

// ImageStore lets an operator manage the images a backend starts sandboxes
// from, rather than only pulling one when a sandbox first asks for it: list
// those ready, install one ahead of use (pull it, and build its root disk
// where the backend has one), and remove one. What is installed survives a
// restart of sandboxd; the backend's own storage is the record.
type ImageStore interface {
	Images(ctx context.Context) ([]ImageInfo, error)
	// InstallImage makes ref ready to start sandboxes from, reporting how far
	// it has got to progress, which may be nil. Installing one already
	// installed checks it against the registry and is otherwise quick.
	InstallImage(ctx context.Context, ref string, progress func(ImageProgress)) error
	// RemoveImage removes ref and what only it needed, returning the bytes
	// freed: ErrNotFound if it is not installed, ErrBusy if a sandbox
	// running or suspended here starts from it.
	RemoveImage(ctx context.Context, ref string) (int64, error)
}

// ImageInfo is one installed image.
type ImageInfo struct {
	Ref    string
	Digest string // "" where the backend does not say
	// Bytes is what it takes on the host — its root disk, on Linux — or 0
	// where the backend does not say. Layers shared with other images are
	// not counted.
	Bytes       int64
	InstalledAt time.Time // zero where the backend does not say
}

// ImageProgress is how far an install has got.
type ImageProgress struct {
	Phase       string // "pulling", then "building" where there is a disk to build
	Done, Total int64  // bytes pulled of the image's total; 0 where unknown
}

// Suspender can stop a sandbox and bring it back later with its memory,
// processes and disk exactly as they were, costing no CPU or memory meanwhile.
type Suspender interface {
	Suspend(ctx context.Context, id string) error
	Resume(ctx context.Context, id string) error
}

// Snapshotter can capture a running sandbox without stopping it, and start
// new sandboxes from the capture (Spec.FromSnapshot): memory and disk where
// the backend has CapMemorySnapshot, the files only where it has
// CapDiskSnapshot.
type Snapshotter interface {
	Snapshot(ctx context.Context, id, snapshotID string) (SnapshotInfo, error)
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// SnapshotLister can say which snapshots it holds on disk, by id. A server
// that keeps its records across a restart pairs them with its own and deletes
// the rest: a snapshot no record names is one nobody can use or remove.
type SnapshotLister interface {
	StoredSnapshots() []string
}

// SnapshotInfo describes a capture.
type SnapshotInfo struct {
	ID string
	// Kind is api.SnapshotMemory or api.SnapshotDisk.
	Kind     string
	Bytes    int64 // on the host's disk
	Image    string
	CPUs     float64
	MemoryMB int
	DiskMB   int
}

// SnapshotProgress is what a Snapshotter reports as it goes: the phase
// (api.SnapshotPhaseCapture, then api.SnapshotPhaseStore), the bytes read of
// the sandbox, and an estimate of the total, 0 when there is none.
type SnapshotProgress struct {
	Phase          string
	Bytes          int64
	EstimatedBytes int64
}

type snapshotProgressKey struct{}

// WithSnapshotProgress has Snapshot calls under ctx report to fn. A backend
// that does not report leaves the caller with the phase it started in.
func WithSnapshotProgress(ctx context.Context, fn func(SnapshotProgress)) context.Context {
	return context.WithValue(ctx, snapshotProgressKey{}, fn)
}

// ReportSnapshotProgress is how a Snapshotter reports; without a listener it
// does nothing.
func ReportSnapshotProgress(ctx context.Context, p SnapshotProgress) {
	if fn, ok := ctx.Value(snapshotProgressKey{}).(func(SnapshotProgress)); ok {
		fn(p)
	}
}

// ImageLister can say which images it already has a root disk for, so a
// create naming one skips the pull and the build. A gateway in front of many
// nodes prefers such a node (GET /v1/node). It is a placement hint, not a
// promise: an image reported here may be evicted, or rebuilt for a new guest
// agent, before the create arrives.
type ImageLister interface {
	CachedImages() []string
}

// Dialer can open a TCP connection to a port on the guest's own loopback —
// a tunnel, which is how a dev server in a sandbox is reached without
// publishing anything on the network.
type Dialer interface {
	DialGuest(ctx context.Context, id string, port int) (io.ReadWriteCloser, error)
}

// ProcSpec is one process to start inside a sandbox. Env is merged over the
// sandbox's own by the server before it gets here.
type ProcSpec struct {
	Argv []string
	Env  map[string]string
	Cwd  string
	// Tty runs the process on a terminal of Rows x Cols; its stdout and stderr
	// are then one stream, delivered on stdout.
	Tty        bool
	Rows, Cols uint16
	// Keep keeps the process running when the server goes, under Session,
	// an id the server chooses: a Reattacher takes it back after a restart.
	// Only asked for by a server that keeps its sandboxes.
	Keep    bool
	Session string
}

// Reattacher can take back a process started with ProcSpec.Keep, after the
// server that started it has gone: its output — what the sandbox kept of it,
// then live — goes to stdout and stderr, and dropped, if not nil, is told how
// many bytes from before that were not kept. A session the sandbox does not
// hold is ErrNotFound.
type Reattacher interface {
	Reattach(ctx context.Context, id, session string, stdout, stderr io.Writer, dropped func(int64)) (Proc, error)
}

// Resizer is a Proc on a terminal whose size can change.
type Resizer interface {
	Resize(rows, cols uint16) error
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
	ErrReadOnly    = errors.New("read-only file system")
	ErrNoSuchCmd   = errors.New("no such command")
	ErrNoSuchCwd   = errors.New("no such working directory")
	ErrBadSignal   = errors.New("unsupported signal")
	ErrUnavailable = errors.New("backend unavailable")
	ErrBusy        = errors.New("busy")
	ErrUnsupported = errors.New("unsupported for this sandbox")
	// ErrNoSpace: the host has too little free disk for what was asked. Its
	// message may say how much is free and needed — sizes, never paths.
	ErrNoSpace = errors.New("the host has too little free disk")
)

// Usage is a sandbox's resource use, as the host measures it.
type Usage struct {
	CPUUsec          int64 // CPU time used since the sandbox started, in microseconds
	MemoryBytes      int64
	MemoryLimitBytes int64
	NetRxBytes       int64
	NetTxBytes       int64
	DiskReadBytes    int64
	DiskWriteBytes   int64
	Processes        int
}

// UsageReader can say how much of the host each of its running sandboxes is
// using, from outside the guest. All at once: one reading per sample, however
// many sandboxes there are.
type UsageReader interface {
	Usage(ctx context.Context) (map[string]Usage, error)
}
