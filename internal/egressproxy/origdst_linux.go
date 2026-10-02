//go:build linux

package egressproxy

import (
	"errors"
	"net"
	"syscall"
	"unsafe"
)

// OriginalDestination returns the address a redirected connection was originally
// aimed at, before iptables sent it here.
//
// REDIRECT rewrites the destination, so by the time the proxy accepts the socket
// its own address is all that LocalAddr can report. The kernel keeps the original
// in a socket option, and SO_ORIGINAL_DST is the only way back to it.
//
// It is used for the *port*, not the address: which port was asked for decides
// how to talk upstream (443 and 80 behave differently), while where to connect
// comes from resolving the name the client announced. Trusting the original
// address would defeat the point — the whole reason this package exists is that
// an address is not a name.
func OriginalDestination(c net.Conn) (net.IP, int, error) {
	tcp, ok := c.(*net.TCPConn)
	if !ok {
		return nil, 0, errors.New("not a TCP connection")
	}
	raw, err := tcp.SyscallConn()
	if err != nil {
		return nil, 0, err
	}

	var addr syscall.RawSockaddrInet4
	size := uint32(unsafe.Sizeof(addr))
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		// SOL_IP is 0; SO_ORIGINAL_DST is 80 on Linux. They are spelled out rather
		// than taken from x/sys so this stays on the standard library.
		_, _, e := syscall.Syscall6(
			syscall.SYS_GETSOCKOPT, fd, 0, 80,
			uintptr(unsafe.Pointer(&addr)), uintptr(unsafe.Pointer(&size)), 0)
		if e != 0 {
			sockErr = e
		}
	})
	if err != nil {
		return nil, 0, err
	}
	if sockErr != nil {
		return nil, 0, sockErr
	}
	// The port is network byte order in the raw structure.
	port := int(addr.Port>>8) | int(addr.Port&0xff)<<8
	ip := net.IPv4(addr.Addr[0], addr.Addr[1], addr.Addr[2], addr.Addr[3])
	return ip, port, nil
}
