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
//
// The accepted descriptor is non-blocking, which is what makes os.NewFile hand
// it to the runtime's poller. That matters for Close: on a blocking descriptor
// the runtime defers the real close(2) until any read blocked on it returns —
// and a read on a connection the other side is waiting to see closed never
// returns, so the peer never got its EOF. Found by a tunnel that delivered its
// reply and then hung.
func (l *Listener) Accept() (net.Conn, error) {
	nfd, _, e := syscall.Syscall6(syscall.SYS_ACCEPT4, uintptr(l.fd), 0, 0, syscall.SOCK_CLOEXEC|syscall.SOCK_NONBLOCK, 0, 0)
	if e != 0 {
		return nil, e
	}
	return &conn{File: os.NewFile(nfd, "vsock")}, nil
}

// CloseWrite half-closes the connection: the peer reads EOF, and can still send.
func (c *conn) CloseWrite() error {
	sc, err := c.File.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := sc.Control(func(fd uintptr) { serr = syscall.Shutdown(int(fd), syscall.SHUT_WR) }); err != nil {
		return err
	}
	return serr
}

// Close implements net.Listener.
func (l *Listener) Close() error { return syscall.Close(l.fd) }

// Addr implements net.Listener.
func (l *Listener) Addr() net.Addr { return addr(l.port) }

type addr uint32

func (a addr) Network() string { return "vsock" }
func (a addr) String() string  { return fmt.Sprintf("vsock:%d", uint32(a)) }

// conn adapts an accepted vsock fd to net.Conn.
type conn struct{ *os.File }

func (c *conn) LocalAddr() net.Addr                { return addr(0) }
func (c *conn) RemoteAddr() net.Addr               { return addr(0) }
func (c *conn) SetDeadline(t time.Time) error      { return c.File.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.File.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.File.SetWriteDeadline(t) }
