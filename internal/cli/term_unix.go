//go:build linux || darwin

package cli

import (
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&t)))
	return e == 0
}

// termSize is f's size in rows and columns, or 24x80 when unknown.
func termSize(f *os.File) (uint16, uint16) {
	ws := struct{ row, col, x, y uint16 }{}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws))); e != 0 || ws.row == 0 {
		return 24, 80
	}
	return ws.row, ws.col
}

// makeRaw puts the terminal in raw mode — every key goes to the sandbox,
// ^C included — and returns how to put it back.
func makeRaw(f *os.File) (func(), error) {
	var old syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlGetTermios, uintptr(unsafe.Pointer(&old))); e != 0 {
		return nil, e
	}
	raw := old
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlSetTermios, uintptr(unsafe.Pointer(&raw))); e != 0 {
		return nil, e
	}
	return func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), ioctlSetTermios, uintptr(unsafe.Pointer(&old)))
	}, nil
}

// onResize calls fn whenever the terminal is resized, until stop is called.
func onResize(fn func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
				fn()
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(ch); close(done) }
}
