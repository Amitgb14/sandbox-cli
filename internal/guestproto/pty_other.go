//go:build !linux

package guestproto

import (
	"errors"
	"os"
	"syscall"
)

type pty struct{ master, slave *os.File }

func openPTY(uint16, uint16) (*pty, error) {
	return nil, errors.New("terminals are allocated only inside a Linux guest")
}
func (p *pty) resize(uint16, uint16) {}
func (p *pty) close()                {}
func ttyAttr(*syscall.SysProcAttr)   {}
