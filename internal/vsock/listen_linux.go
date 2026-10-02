package vsock

import (
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	afVsock = 40
	cidAny  = 0xFFFFFFFF
)

type sockaddrVM struct {
	family    uint16
	reserved1 uint16
	port      uint32
	cid       uint32
	zero      [4]uint8
}

// Listener accepts vsock connections inside a guest.
type Listener struct {
	fd   int
	port uint32
}

// Listen listens on a vsock port, any CID.
func Listen(port uint32) (*Listener, error) {
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}
	sa := sockaddrVM{family: afVsock, port: port, cid: cidAny}
	if _, _, e := syscall.Syscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa)); e != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("bind vsock port %d: %w", port, e)
	}
	if err := syscall.Listen(fd, 64); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("listen: %w", err)
	}
	return &Listener{fd: fd, port: port}, nil
}

// Accept implements net.Listener.
func (l *Listener) Accept() (net.Conn, error) {
	nfd, _, e := syscall.Syscall6(syscall.SYS_ACCEPT4, uintptr(l.fd), 0, 0, syscall.SOCK_CLOEXEC, 0, 0)
	if e != 0 {
		return nil, e
	}
	return &conn{File: os.NewFile(nfd, "vsock")}, nil
}

// Close implements net.Listener.
func (l *Listener) Close() error { return syscall.Close(l.fd) }

// Addr implements net.Listener.
func (l *Listener) Addr() net.Addr { return addr(l.port) }

type addr uint32

func (a addr) Network() string { return "vsock" }
func (a addr) String() string  { return fmt.Sprintf("vsock:%d", uint32(a)) }

// conn adapts an accepted vsock fd to net.Conn. Deadlines are not supported;
// the guest agent bounds nothing by time — the host does.
type conn struct{ *os.File }

func (c *conn) LocalAddr() net.Addr              { return addr(0) }
func (c *conn) RemoteAddr() net.Addr             { return addr(0) }
func (c *conn) SetDeadline(time.Time) error      { return nil }
func (c *conn) SetReadDeadline(time.Time) error  { return nil }
func (c *conn) SetWriteDeadline(time.Time) error { return nil }
