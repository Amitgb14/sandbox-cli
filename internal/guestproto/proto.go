// Package guestproto is how the host talks to sandbox-guestd inside a VM — the
// only way it does.
//
// The transport is any byte stream: Firecracker's vsock bridge on Linux, the
// stdio of `container exec -i … sandbox-guestd serve --stdio` on macOS, a unix
// socket in tests. Each connection carries exactly one request:
//
//	host  -> guest   one JSON line: Request
//	guest -> host    one JSON line: Response (ok, or an error)
//	then, per op:
//	  read    guest sends Response.Size raw bytes
//	  write   host sends Request.Size raw bytes, guest answers a second Response
//	  exec    frames both ways until the guest sends FrameExit
//	  attach  the same, on a kept process (below)
//
// A process the host starts with Keep outlives the connection that started
// it: its output is buffered in the guest, and a later connection rejoins it
// by its session id with OpAttach. That is how a restarted sandboxd takes back
// the processes of a sandbox it kept running (sandboxd --keep-sandboxes).
// Without Keep a process ends with its connection, as it always has.
//
// A frame is a type byte, a big-endian uint32 length, and that many bytes.
//
// The host treats everything the guest sends as untrusted — the guest is where
// the agent runs. Every length is bounded before it is allocated or read, and a
// guest that breaks the protocol gets its connection closed, never a bigger
// buffer.
package guestproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Version is sent in a ping reply, so the host can tell an out-of-date guest
// agent from a broken one.
const Version = "2"

// Ops.
const (
	OpPing   = "ping"
	OpExec   = "exec"
	OpRead   = "read"
	OpWrite  = "write"
	OpRemove = "remove"
	OpList   = "list"
	// OpDial connects to a port on the guest's loopback and, after the answer
	// line, carries that connection's raw bytes both ways.
	OpDial = "dial"
	// OpSync flushes the guest's filesystems to their disks. The host asks
	// before it stops a VM that has volumes: a VMM that is killed takes the
	// guest's page cache with it, and a volume is meant to outlive the VM.
	OpSync = "sync"
	// OpAttach rejoins a kept process by its session id: the buffered output
	// from Request.Offset on, then live, then FrameExit — the frames of exec.
	OpAttach = "attach"
	// OpSessions lists the kept processes the guest holds.
	OpSessions = "sessions"
)

// Request is the first line of every connection.
type Request struct {
	Op   string            `json:"op"`
	Argv []string          `json:"argv,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Path string            `json:"path,omitempty"`
	Size int64             `json:"size,omitempty"`
	// Tty runs an exec on a pseudo-terminal of Rows x Cols: stdout and stderr
	// become one stream, and FrameResize changes the size.
	Port int    `json:"port,omitempty"`
	Tty  bool   `json:"tty,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	// Final, on a sync, says the VM is about to stop for good: the guest also
	// remounts its volumes read-only, which commits their journals and marks
	// them clean, so the next VM can mount one read-only. A sync before a
	// suspend is not final — the sandbox runs again afterwards.
	Final bool `json:"final,omitempty"`
	// Keep, on an exec, keeps the process running when the connection goes,
	// under Session, an id the host chooses (SessionIDOK). Offset, on an
	// attach, is how many bytes of output the host already has.
	Keep    bool   `json:"keep,omitempty"`
	Session string `json:"session,omitempty"`
	Offset  int64  `json:"offset,omitempty"`
}

// SessionInfo is one kept process.
type SessionInfo struct {
	ID       string `json:"id"`
	Running  bool   `json:"running"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// SessionIDOK reports whether s is a session id a host may choose: 1 to 64
// letters, digits, '-' and '_'.
func SessionIDOK(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Response is the guest's answer line.
type Response struct {
	OK      bool       `json:"ok"`
	Error   *Error     `json:"error,omitempty"`
	Version string     `json:"version,omitempty"`
	Size    int64      `json:"size,omitempty"`
	Entries []DirEntry `json:"entries,omitempty"`
	// Sessions answers OpSessions.
	Sessions []SessionInfo `json:"sessions,omitempty"`
}

// DirEntry is one entry of a listing.
type DirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file", "dir", "symlink" or "other"
	Size int64  `json:"size"`
}

// Error codes the guest may answer with. Anything else is reported as internal.
const (
	CodeNotFound      = "not_found"
	CodeIsDir         = "is_dir"
	CodeNotDir        = "not_dir"
	CodeNotEmpty      = "not_empty"
	CodeReadOnly      = "read_only"
	CodeNoSuchCommand = "no_such_command"
	CodeNoSuchCwd     = "no_such_cwd"
	CodeBadRequest    = "bad_request"
	CodeTooLarge      = "too_large"
	CodeInternal      = "internal"
)

// Error is a guest-side failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return "guest: " + e.Code + ": " + e.Message }

// IsCode reports whether err is a guest error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Frame types.
const (
	FrameStdin    byte = 'i' // host -> guest: bytes for stdin
	FrameStdinEOF byte = 'e' // host -> guest: close stdin
	FrameSignal   byte = 's' // host -> guest: signal name ("TERM")
	FrameResize   byte = 'r' // host -> guest: rows, cols (uint16 each); for a tty
	FrameStdout   byte = 'o' // guest -> host
	FrameStderr   byte = 'E' // guest -> host
	FrameExit     byte = 'x' // guest -> host: int32 exit code; last frame
	// FrameDropped, on an attach, says output older than the guest keeps was
	// dropped before the replay that follows: uint64 bytes dropped.
	FrameDropped byte = 'd'
)

// Limits. The guest is untrusted, so the host refuses anything larger before it
// reads it.
const (
	MaxLine      = 1 << 20  // one JSON line
	MaxFrame     = 1 << 20  // one frame's payload
	MaxFileBytes = 64 << 20 // one read or write
)

// WriteFrame writes one frame.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame of %d bytes exceeds %d", len(payload), MaxFrame)
	}
	var hdr [5]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame reads one frame, refusing one larger than MaxFrame before
// allocating for it.
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame of %d bytes exceeds %d", n, MaxFrame)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return hdr[0], buf, nil
}
