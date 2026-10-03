package guestproto

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// pty is a pseudo-terminal pair, allocated by hand because the standard library
// has none and the module takes no dependencies.
type pty struct {
	master, slave *os.File
}

func openPTY(rows, cols uint16) (*pty, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var unlock int32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		m.Close()
		return nil, e
	}
	var n uint32
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); e != 0 {
		m.Close()
		return nil, e
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		m.Close()
		return nil, err
	}
	p := &pty{master: m, slave: s}
	if rows == 0 || cols == 0 {
		rows, cols = 24, 80
	}
	p.resize(rows, cols)
	return p, nil
}

func (p *pty) resize(rows, cols uint16) {
	ws := struct{ row, col, x, y uint16 }{rows, cols, 0, 0}
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, p.master.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

func (p *pty) close() {
	p.master.Close()
	p.slave.Close()
}

// ttyAttr makes the slave the process's controlling terminal, in a session of
// its own (which also makes it its own process group).
func ttyAttr(a *syscall.SysProcAttr) {
	a.Setsid = true
	a.Setctty = true
	a.Setpgid = false // Setsid already leads a new group; both together is EPERM
	a.Ctty = 0        // fd 0 of the child: the slave
}
