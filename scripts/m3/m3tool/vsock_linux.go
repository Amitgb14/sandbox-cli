package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// AF_VSOCK by hand, because the standard library has no vsock sockaddr and the
// module takes no dependencies. Only what a listener needs.

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

type vsockListener struct{ fd int }

func listenVsock(port uint32) (*vsockListener, error) {
	fd, err := syscall.Socket(afVsock, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("socket(AF_VSOCK): %w", err)
	}
	sa := sockaddrVM{family: afVsock, port: port, cid: cidAny}
	if _, _, e := syscall.Syscall(syscall.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(&sa)), unsafe.Sizeof(sa)); e != 0 {
		return nil, fmt.Errorf("bind vsock port %d: %w", port, e)
	}
	if err := syscall.Listen(fd, 16); err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	return &vsockListener{fd: fd}, nil
}

func (l *vsockListener) accept() (*os.File, error) {
	nfd, _, e := syscall.Syscall6(syscall.SYS_ACCEPT4, uintptr(l.fd), 0, 0, 0, 0, 0)
	if e != 0 {
		return nil, e
	}
	return os.NewFile(nfd, "vsock"), nil
}
