//go:build unix

package guestproto

import (
	"os"
	"os/exec"
	"syscall"
)

var (
	errnoNotDir   = syscall.ENOTDIR
	errnoNotEmpty = syscall.ENOTEMPTY
)

var signals = map[string]syscall.Signal{
	"INT": syscall.SIGINT, "TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "HUP": syscall.SIGHUP,
}

// procAttr puts each process in its own group, so a signal reaches what it
// started too, and drops to the sandbox user when the agent runs as root.
func (s *Server) procAttr() *syscall.SysProcAttr {
	a := &syscall.SysProcAttr{Setpgid: true}
	if os.Getuid() == 0 && s.UID >= 0 && s.GID >= 0 {
		a.Credential = &syscall.Credential{Uid: uint32(s.UID), Gid: uint32(s.GID)}
	}
	return a
}

func (s *Server) chown(p string) {
	if os.Getuid() == 0 && s.UID >= 0 && s.GID >= 0 {
		_ = os.Lchown(p, s.UID, s.GID)
	}
}

// killGroup signals the process's whole group. An unknown name is ignored: the
// host validated it, and a guest agent that guessed would be inventing policy.
func killGroup(cmd *exec.Cmd, name string) {
	sig, ok := signals[name]
	if !ok || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
