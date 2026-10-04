package api

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// StreamProtocol is the Upgrade token for an attach stream.
const StreamProtocol = "sbx-stream/1"

// TunnelProtocol is the Upgrade token for a tunnel: after the 101, the
// connection is the raw bytes of a TCP connection to the guest's port.
const TunnelProtocol = "sbx-tunnel/1"

// Tunnel opens a TCP connection to port on the sandbox's own loopback.
func (c *Client) Tunnel(ctx context.Context, ref string, port int) (io.ReadWriteCloser, error) {
	return c.upgrade(ctx, sbx(ref)+"/tunnel?port="+strconv.Itoa(port), TunnelProtocol)
}

// Stream is an attached process: what the process writes arrives through Copy,
// and Stdin, Resize and Signal reach it. Close detaches without stopping it.
type Stream struct {
	rwc io.ReadWriteCloser
	mu  sync.Mutex
}

// Attach opens a two-way stream on a running process. Output is replayed from
// the beginning.
func (c *Client) Attach(ctx context.Context, ref string, pid int) (*Stream, error) {
	rwc, err := c.upgrade(ctx, sbx(ref)+"/processes/"+strconv.Itoa(pid)+"/attach", StreamProtocol)
	if err != nil {
		return nil, err
	}
	return &Stream{rwc: rwc}, nil
}

// upgrade performs a GET that switches the connection to protocol.
func (c *Client) upgrade(ctx context.Context, path, protocol string) (io.ReadWriteCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", protocol)
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		var eb ErrorBody
		if jsonErr := decodeErr(data, &eb); jsonErr == nil && eb.Error.Code != "" {
			return nil, &Error{Status: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
		}
		return nil, fmt.Errorf("%s: %s", protocol, resp.Status)
	}
	rwc, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: the connection was not upgraded", protocol)
	}
	return rwc, nil
}

func (s *Stream) send(typ byte, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return guestproto.WriteFrame(s.rwc, typ, b)
}

// Write sends bytes to the process's stdin.
func (s *Stream) Write(p []byte) (int, error) {
	for off := 0; off < len(p); {
		n := min(len(p)-off, guestproto.MaxFrame)
		if err := s.send(guestproto.FrameStdin, p[off:off+n]); err != nil {
			return off, err
		}
		off += n
	}
	return len(p), nil
}

// CloseStdin ends the process's input (^D on a terminal).
func (s *Stream) CloseStdin() error { return s.send(guestproto.FrameStdinEOF, nil) }

// Resize changes the process's terminal size.
func (s *Stream) Resize(rows, cols uint16) error {
	var b [4]byte
	binary.BigEndian.PutUint16(b[0:2], rows)
	binary.BigEndian.PutUint16(b[2:4], cols)
	return s.send(guestproto.FrameResize, b[:])
}

// Signal delivers one of Signals.
func (s *Stream) Signal(sig string) error { return s.send(guestproto.FrameSignal, []byte(sig)) }

// Copy writes the process's output to stdout and stderr until it exits, and
// returns its exit code; or returns an error when the stream ends first.
func (s *Stream) Copy(stdout, stderr io.Writer) (int, error) {
	for {
		typ, payload, err := guestproto.ReadFrame(s.rwc)
		if err != nil {
			return -1, err
		}
		switch typ {
		case guestproto.FrameStdout:
			_, _ = stdout.Write(payload)
		case guestproto.FrameStderr:
			_, _ = stderr.Write(payload)
		case guestproto.FrameExit:
			if len(payload) != 4 {
				return -1, fmt.Errorf("malformed exit frame")
			}
			return int(int32(binary.BigEndian.Uint32(payload))), nil
		}
	}
}

// Close detaches. The process keeps running.
func (s *Stream) Close() error { return s.rwc.Close() }
