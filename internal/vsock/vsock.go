// Package vsock is the host<->guest socket: listening on a vsock port inside a
// Linux guest, and dialling one from the host through Firecracker's unix-socket
// bridge.
//
// Hand-written over syscall because the standard library has no vsock sockaddr
// and the module takes no dependencies. Measured in M3 at ~0.1 ms per fresh
// connection and 1.3-1.7 GB/s.
package vsock

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// DialFirecracker opens a connection to a guest's vsock port through the VMM's
// unix socket: connect, send "CONNECT <port>\n", expect "OK <n>\n". What comes
// after is the guest's stream.
func DialFirecracker(ctx context.Context, uds string, port uint32) (io.ReadWriteCloser, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", uds)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if _, err := fmt.Fprintf(c, "CONNECT %d\n", port); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(c, 4096)
	line, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "OK ") {
		c.Close()
		return nil, fmt.Errorf("vsock bridge refused port %d: %q (%v)", port, strings.TrimSpace(line), err)
	}
	_ = c.SetDeadline(time.Time{})
	return &bridged{Conn: c, r: br}, nil
}

// bridged keeps the bytes the handshake reader may have buffered past "OK".
type bridged struct {
	net.Conn
	r *bufio.Reader
}

func (b *bridged) Read(p []byte) (int, error) { return b.r.Read(p) }

// CloseWrite half-closes toward the guest.
func (b *bridged) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}
