//go:build !unix

package guestproto

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// The guest agent runs in a Linux VM. These keep the package compiling where
// the host client is built.

var (
	errnoNotDir   = errors.New("not a directory")
	errnoNotEmpty = errors.New("directory not empty")
	errnoReadOnly = errors.New("read-only file system")
)

func syncFilesystems(bool) {}

func (s *Server) procAttr() *syscall.SysProcAttr { return nil }
func (s *Server) chown(string)                   {}

func killGroup(cmd *exec.Cmd, name string) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func exitCode(ps *os.ProcessState) int {
	if ps == nil {
		return -1
	}
	return ps.ExitCode()
}
