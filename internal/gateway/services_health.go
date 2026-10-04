package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Health checks go through the guest agent, as every other call does: an
// HTTP probe over the node's tunnel to the replica's own loopback, or a
// command run in it. Neither needs the guest to have a network, and neither
// lets a replica's answer reach anything but a yes or a no — and a short,
// cleaned reason for the record.

// replicaHealth is what the controller knows of one replica, in memory.
type replicaHealth struct {
	checking  bool // a check is running
	checked   bool // at least one has finished
	healthy   bool
	fails     int // failed checks in a row
	lastCheck time.Time
	lastErr   string
	lost      bool // its node is not answering
	fatal     bool // its sandbox is gone or its command exited: replaced at once
}

// checkResult is one check's outcome.
type checkResult struct {
	ok    bool
	fatal bool
	err   string
}

func (h *replicaHealth) apply(r checkResult, at time.Time) {
	h.checking, h.checked, h.lastCheck = false, true, at
	switch {
	case r.fatal:
		h.fatal, h.healthy, h.lastErr = true, false, r.err
	case r.ok:
		h.healthy, h.fails, h.lastErr = true, 0, ""
	default:
		h.healthy, h.lastErr = false, r.err
		h.fails++
	}
}

// check is one check of one replica: its command still running, if it has
// one, and then its health check, if it has one.
func (g *Gateway) check(ctx context.Context, n *node, m replicaRecord, sp api.ServiceSpec) checkResult {
	timeout := 5 * time.Second
	if sp.Health != nil {
		timeout = time.Duration(sp.Health.TimeoutSecs) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if m.PID > 0 {
		p, err := n.client.Process(ctx, m.Sandbox, m.PID)
		switch {
		case api.IsCode(err, api.CodeNotFound):
			return checkResult{fatal: true, err: "the sandbox or its command is gone"}
		case err != nil:
			return checkResult{err: reason(err)}
		case p.State == api.ProcessExited:
			code := "?"
			if p.ExitCode != nil {
				code = strconv.Itoa(*p.ExitCode)
			}
			return checkResult{fatal: true, err: "the command exited with status " + code}
		}
	}
	h := sp.Health
	switch {
	case h == nil:
		return checkResult{ok: true}
	case h.HTTP != "":
		return g.probeHTTP(ctx, n, m.Sandbox, sp.Port, h.HTTP, timeout)
	default:
		res, err := n.client.Run(ctx, m.Sandbox, api.RunRequest{Argv: h.Command, TimeoutSecs: h.TimeoutSecs})
		switch {
		case api.IsCode(err, api.CodeNotFound):
			return checkResult{fatal: true, err: "the sandbox is gone"}
		case err != nil:
			return checkResult{err: reason(err)}
		case res.TimedOut:
			return checkResult{err: "the health command timed out"}
		case res.ExitCode != 0:
			return checkResult{err: fmt.Sprintf("the health command exited with status %d", res.ExitCode)}
		}
		return checkResult{ok: true}
	}
}

// probeHTTP asks GET path of the replica's port, through a tunnel. A 2xx or
// 3xx within the timeout is healthy; a redirect is not followed.
func (g *Gateway) probeHTTP(ctx context.Context, n *node, id string, port int, path string, timeout time.Duration) checkResult {
	tr := &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return dialTunnel(ctx, n, id, port) },
		DisableKeepAlives: true,
	}
	defer tr.CloseIdleConnections()
	c := &http.Client{Transport: tr, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost"+path, nil)
	if err != nil {
		return checkResult{err: "health.http: " + err.Error()}
	}
	req.Header.Set("User-Agent", "sandbox-gateway-health")
	resp, err := c.Do(req)
	if err != nil {
		if api.IsCode(err, api.CodeNotFound) {
			return checkResult{fatal: true, err: "the sandbox is gone"}
		}
		return checkResult{err: reason(err)}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return checkResult{err: "GET " + termsafe.Clean(path) + " answered " + strconv.Itoa(resp.StatusCode)}
	}
	return checkResult{ok: true}
}

// reason is an error as the record keeps it: one line, cleaned of whatever
// a guest or a node put in it, and short.
func reason(err error) string {
	var ae *api.Error
	msg := err.Error()
	if errors.As(err, &ae) {
		msg = ae.Code + ": " + ae.Message
	}
	msg = termsafe.Clean(msg)
	if r := []rune(msg); len(r) > 200 {
		msg = string(r[:200]) + "…"
	}
	return msg
}

// dialTunnel opens a connection to port on a replica's loopback through its
// node, as the node's own client: the caller's credential never reaches the
// node.
func dialTunnel(ctx context.Context, n *node, id string, port int) (net.Conn, error) {
	rwc, err := n.client.Tunnel(ctx, id, port)
	if err != nil {
		return nil, err
	}
	return &tunnelConn{ReadWriteCloser: rwc, id: id, port: port}, nil
}

// tunnelConn is a tunnel as a net.Conn, for an http.Transport. It has no
// deadlines of its own; the transport's timeouts close it instead.
type tunnelConn struct {
	io.ReadWriteCloser
	id   string
	port int
}

type tunnelAddr string

func (a tunnelAddr) Network() string { return "sbx-tunnel" }
func (a tunnelAddr) String() string  { return string(a) }

func (c *tunnelConn) LocalAddr() net.Addr { return tunnelAddr("gateway") }
func (c *tunnelConn) RemoteAddr() net.Addr {
	return tunnelAddr(c.id + ":" + strconv.Itoa(c.port))
}
func (c *tunnelConn) SetDeadline(time.Time) error      { return nil }
func (c *tunnelConn) SetReadDeadline(time.Time) error  { return nil }
func (c *tunnelConn) SetWriteDeadline(time.Time) error { return nil }
