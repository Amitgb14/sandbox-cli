package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Error is a non-2xx response, carrying the API's error code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (%d): %s", e.Code, e.Status, e.Message)
}

// IsCode reports whether err is an API error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Client talks to one endpoint. It has no idea which mode is behind it: that is
// what Capabilities is for.
type Client struct {
	base  string // "http://host:port" (or "http://sandboxd" over a unix socket)
	token string
	http  *http.Client
	// org, when set, is sent as OrgHeader on every request, streams and
	// upgrades included: a gateway acts in that organisation.
	org string
}

// WithOrg returns a copy of c that acts in organisation org on a gateway
// (OrgHeader on every request); "" acts in the key's own tenant. The copy
// shares c's connections.
func (c *Client) WithOrg(org string) *Client {
	cp := *c
	cp.org = org
	return &cp
}

// Org is the organisation c selects, "" for none.
func (c *Client) Org() string { return c.org }

// setAuth puts the credential and the organisation on a request.
func (c *Client) setAuth(req *http.Request) {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.org != "" {
		req.Header.Set(OrgHeader, c.org)
	}
}

// NewClient returns a client for endpoint, which is either a URL
// (http://127.0.0.1:7070, https://sandbox.example.internal) or a unix socket
// path (unix:///run/sandboxd.sock). token may be empty.
func NewClient(endpoint, token string) (*Client, error) {
	if path, ok := strings.CutPrefix(endpoint, "unix://"); ok {
		if path == "" {
			return nil, fmt.Errorf("endpoint %q names no socket", endpoint)
		}
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", path)
			},
		}
		// The host part is never dialled; it only has to be a loopback name so the
		// server's DNS-rebinding guard accepts it.
		return &Client{base: "http://localhost", token: token, http: &http.Client{Transport: tr}}, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("endpoint %q: want http(s)://host[:port] or unix:///path", endpoint)
	}
	return &Client{base: strings.TrimSuffix(u.String(), "/"), token: token, http: &http.Client{}}, nil
}

// NewClientWithCA is NewClient for an https endpoint whose certificate is
// signed by a private CA — the usual self-hosted case. caPEM holds the CA
// certificates to trust, in addition to none of the system's: a self-hosted
// endpoint is reached by its own CA only.
func NewClientWithCA(endpoint, token string, caPEM []byte) (*Client, error) {
	c, err := NewClient(endpoint, token)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no certificates found in the CA file")
	}
	c.http = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	return c, nil
}

// NewClientWithHTTP is NewClient with a caller-supplied http.Client, for tests
// that serve the API in-process.
func NewClientWithHTTP(base, token string, hc *http.Client) *Client {
	return &Client{base: strings.TrimSuffix(base, "/"), token: token, http: hc}
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.setAuth(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var eb ErrorBody
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		if json.Unmarshal(data, &eb) != nil || eb.Error.Code == "" {
			eb.Error = ErrorDetail{Code: CodeInternal, Message: strings.TrimSpace(string(data))}
		}
		return nil, &Error{Status: resp.StatusCode, Code: eb.Error.Code, Message: eb.Error.Message}
	}
	return resp, nil
}

func (c *Client) json(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, ct = bytes.NewReader(data), "application/json"
	}
	resp, err := c.do(ctx, method, path, nil, body, ct)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func sbx(ref string) string { return "/v1/sandboxes/" + url.PathEscape(ref) }

// Transport is what a reverse proxy needs to reach this endpoint as this client
// does: the base URL, the transport (a unix socket dialer, or TLS trusting a
// private CA), and the token to present. Studio's local server uses it so the
// browser never holds the endpoint's token.
func (c *Client) Transport() (base *url.URL, rt http.RoundTripper, token string) {
	u, _ := url.Parse(c.base)
	rt = c.http.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	return u, rt, c.token
}

// Capabilities reports what the endpoint can do.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	var out Capabilities
	return out, c.json(ctx, http.MethodGet, "/v1/capabilities", nil, &out)
}

// CreateSandbox creates a sandbox and returns it running.
func (c *Client) CreateSandbox(ctx context.Context, req CreateSandboxRequest) (Sandbox, error) {
	var out Sandbox
	return out, c.json(ctx, http.MethodPost, "/v1/sandboxes", req, &out)
}

// Sandbox returns one sandbox by id or name.
func (c *Client) Sandbox(ctx context.Context, ref string) (Sandbox, error) {
	var out Sandbox
	return out, c.json(ctx, http.MethodGet, sbx(ref), nil, &out)
}

// Sandboxes lists this endpoint's sandboxes, newest first. labels, when
// given as key=value strings, keep only sandboxes carrying every one.
func (c *Client) Sandboxes(ctx context.Context, labels ...string) ([]Sandbox, error) {
	var out SandboxList
	path := "/v1/sandboxes"
	if len(labels) > 0 {
		path += "?" + url.Values{"label": labels}.Encode()
	}
	err := c.json(ctx, http.MethodGet, path, nil, &out)
	return out.Sandboxes, err
}

// CreateVolume creates a named volume (capability volumes).
func (c *Client) CreateVolume(ctx context.Context, req CreateVolumeRequest) (Volume, error) {
	var out Volume
	return out, c.json(ctx, http.MethodPost, "/v1/volumes", req, &out)
}

// Volumes lists the endpoint's volumes, with the sandbox each is attached to.
func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var out VolumeList
	err := c.json(ctx, http.MethodGet, "/v1/volumes", nil, &out)
	return out.Volumes, err
}

// DeleteVolume deletes a volume and everything on it. Refused while attached.
func (c *Client) DeleteVolume(ctx context.Context, name string) error {
	return c.json(ctx, http.MethodDelete, "/v1/volumes/"+url.PathEscape(name), nil, nil)
}

// Events returns a sandbox's audit events, oldest first (capability audit).
func (c *Client) Events(ctx context.Context, ref string) (EventList, error) {
	var out EventList
	return out, c.json(ctx, http.MethodGet, sbx(ref)+"/events", nil, &out)
}

// UpdateSandbox changes a running sandbox.
func (c *Client) UpdateSandbox(ctx context.Context, ref string, req UpdateSandboxRequest) (Sandbox, error) {
	var out Sandbox
	return out, c.json(ctx, http.MethodPatch, sbx(ref), req, &out)
}

// TerminateSandbox terminates a sandbox. Terminating one already terminated
// succeeds.
func (c *Client) TerminateSandbox(ctx context.Context, ref string) error {
	return c.json(ctx, http.MethodDelete, sbx(ref), nil, nil)
}

// Run runs a command to completion.
func (c *Client) Run(ctx context.Context, ref string, req RunRequest) (RunResult, error) {
	var out RunResult
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/run", req, &out)
}

// StartProcess starts a command in the background.
func (c *Client) StartProcess(ctx context.Context, ref string, req RunRequest) (Process, error) {
	var out Process
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/processes", req, &out)
}

// Process returns one process.
func (c *Client) Process(ctx context.Context, ref string, pid int) (Process, error) {
	var out Process
	return out, c.json(ctx, http.MethodGet, sbx(ref)+"/processes/"+strconv.Itoa(pid), nil, &out)
}

// Processes lists a sandbox's processes.
func (c *Client) Processes(ctx context.Context, ref string) ([]Process, error) {
	var out ProcessList
	err := c.json(ctx, http.MethodGet, sbx(ref)+"/processes", nil, &out)
	return out.Processes, err
}

// FollowOutput streams a process's output from the beginning, calling fn for
// each event, until the process exits (the last event carries ExitCode), fn
// returns an error, or ctx ends.
func (c *Client) FollowOutput(ctx context.Context, ref string, pid int, fn func(OutputEvent) error) error {
	resp, err := c.do(ctx, http.MethodGet, sbx(ref)+"/processes/"+strconv.Itoa(pid)+"/output", nil, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var ev OutputEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("decoding output event: %w", err)
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return sc.Err()
}

// WriteStdin writes data to a process's stdin, closing it afterwards when closeAfter
// is set.
func (c *Client) WriteStdin(ctx context.Context, ref string, pid int, data []byte, closeAfter bool) error {
	q := url.Values{}
	if closeAfter {
		q.Set("close", "1")
	}
	resp, err := c.do(ctx, http.MethodPost, sbx(ref)+"/processes/"+strconv.Itoa(pid)+"/stdin", q,
		bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Signal delivers a signal (one of Signals) to a process.
func (c *Client) Signal(ctx context.Context, ref string, pid int, signal string) error {
	return c.json(ctx, http.MethodPost, sbx(ref)+"/processes/"+strconv.Itoa(pid)+"/signal", SignalRequest{Signal: signal}, nil)
}

// Suspend stops a sandbox, keeping its memory, processes and disk.
func (c *Client) Suspend(ctx context.Context, ref string) (Sandbox, error) {
	var out Sandbox
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/suspend", nil, &out)
}

// Resume brings a suspended sandbox back.
func (c *Client) Resume(ctx context.Context, ref string) (Sandbox, error) {
	var out Sandbox
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/resume", nil, &out)
}

// CreateSnapshot captures a running sandbox's memory and disk.
func (c *Client) CreateSnapshot(ctx context.Context, ref string) (Snapshot, error) {
	var out Snapshot
	return out, c.json(ctx, http.MethodPost, sbx(ref)+"/snapshots", nil, &out)
}

// Snapshots lists this endpoint's snapshots.
func (c *Client) Snapshots(ctx context.Context) ([]Snapshot, error) {
	var out SnapshotList
	err := c.json(ctx, http.MethodGet, "/v1/snapshots", nil, &out)
	return out.Snapshots, err
}

// DeleteSnapshot removes a snapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, id string) error {
	return c.json(ctx, http.MethodDelete, "/v1/snapshots/"+url.PathEscape(id), nil, nil)
}

// ReadFile returns a guest file's contents.
func (c *Client) ReadFile(ctx context.Context, ref, path string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, sbx(ref)+"/files", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// WriteFile writes a guest file, creating parent directories.
func (c *Client) WriteFile(ctx context.Context, ref, path string, data []byte) error {
	resp, err := c.do(ctx, http.MethodPut, sbx(ref)+"/files", url.Values{"path": {path}},
		bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// RemoveFile removes a guest file or empty directory.
func (c *Client) RemoveFile(ctx context.Context, ref, path string) error {
	resp, err := c.do(ctx, http.MethodDelete, sbx(ref)+"/files", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// ListDir lists a guest directory.
func (c *Client) ListDir(ctx context.Context, ref, path string) ([]DirEntry, error) {
	resp, err := c.do(ctx, http.MethodGet, sbx(ref)+"/dirs", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out DirList
	return out.Entries, json.NewDecoder(resp.Body).Decode(&out)
}

func decodeErr(data []byte, eb *ErrorBody) error { return json.Unmarshal(data, eb) }
