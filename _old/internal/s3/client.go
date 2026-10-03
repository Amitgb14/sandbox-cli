package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// DefaultAccessKeyEnv and friends are the variable names used when the
// configuration names none. They are the conventional ones, so a machine that
// already has a working `aws` CLI needs no credential configuration here at all.
const (
	DefaultAccessKeyEnv    = "AWS_ACCESS_KEY_ID"
	DefaultSecretKeyEnv    = "AWS_SECRET_ACCESS_KEY"
	DefaultSessionTokenEnv = "AWS_SESSION_TOKEN"
)

// DefaultRegion is what an unset region means. S3-compatible servers that have
// no notion of regions still require *some* region in the signature, and
// "us-east-1" is the value they all accept.
const DefaultRegion = "us-east-1"

// Credentials is one resolved secret triple.
//
// The values live here and nowhere else: they are never logged, never written to
// a config file, never rendered into an API response, and never put on an argv.
// That is the same rule internal/creds keeps and audit.SessionMeta enforces
// structurally by having nowhere to put a value.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Config describes one bucket and how to reach it.
//
// It holds no secret. Credentials are named — AccessKeyEnv is the *name* of an
// environment variable, not its contents — for the reason `gateway:` names the
// variable a gateway key lives in: a config file is a file, it gets committed,
// copied between machines and pasted into issues, and a credential that was
// never in it cannot leak from it.
type Config struct {
	Bucket string
	Region string

	// Endpoint overrides the AWS host, for MinIO, R2, Ceph and the rest. Scheme
	// is optional and defaults to https.
	Endpoint string

	// Prefix is prepended to every key, so one bucket can hold more than
	// snapshots. Leading and trailing slashes are normalised away.
	Prefix string

	// PathStyle addresses the bucket as <endpoint>/<bucket>/<key> rather than
	// <bucket>.<endpoint>/<key>. Required by most self-hosted servers, and by
	// AWS for a bucket whose name is not DNS-safe.
	PathStyle bool

	// The names of the variables holding the credential. Empty means the
	// conventional AWS name.
	AccessKeyEnv    string
	SecretKeyEnv    string
	SessionTokenEnv string
}

// Client talks to one bucket.
type Client struct {
	cfg   Config
	creds Credentials
	http  *http.Client
	// now is a var for the same reason hostTimezone is one: signing is a
	// function of the clock, and a test that cannot pin it can only assert that
	// something was signed.
	now func() time.Time
}

// ErrNoCredentials is returned when the named variables are not set in this
// process's environment.
//
// A distinct error because it is the one failure with an action attached — "set
// $AWS_ACCESS_KEY_ID" — and callers surface it differently from a bucket that
// refused a request.
var ErrNoCredentials = errors.New("no S3 credentials in the environment")

// ErrNotFound is a 404 from the bucket, for a key or for the bucket itself.
var ErrNotFound = errors.New("no such object")

// AccessKeyVar and the other two report which variable name is actually
// consulted, so a UI can say what to set without repeating the defaults.
func (c Config) AccessKeyVar() string    { return orDefault(c.AccessKeyEnv, DefaultAccessKeyEnv) }
func (c Config) SecretKeyVar() string    { return orDefault(c.SecretKeyEnv, DefaultSecretKeyEnv) }
func (c Config) SessionTokenVar() string { return orDefault(c.SessionTokenEnv, DefaultSessionTokenEnv) }

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// Credentials reads the named variables from this process's environment.
//
// A session token is optional — it is only present for temporary credentials —
// but an access key without a secret is a misconfiguration rather than an
// absence, and both spellings come back as ErrNoCredentials with the name of
// what is missing, since "which variable" is the whole of what the reader needs.
func (c Config) Credentials() (Credentials, error) {
	id := os.Getenv(c.AccessKeyVar())
	secret := os.Getenv(c.SecretKeyVar())
	switch {
	case id == "" && secret == "":
		return Credentials{}, fmt.Errorf("%w: set $%s and $%s", ErrNoCredentials, c.AccessKeyVar(), c.SecretKeyVar())
	case id == "":
		return Credentials{}, fmt.Errorf("%w: $%s is set but $%s is not", ErrNoCredentials, c.SecretKeyVar(), c.AccessKeyVar())
	case secret == "":
		return Credentials{}, fmt.Errorf("%w: $%s is set but $%s is not", ErrNoCredentials, c.AccessKeyVar(), c.SecretKeyVar())
	}
	return Credentials{
		AccessKeyID:     id,
		SecretAccessKey: secret,
		SessionToken:    os.Getenv(c.SessionTokenVar()),
	}, nil
}

// New resolves credentials and returns a client for cfg.
func New(cfg Config) (*Client, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("no bucket configured")
	}
	creds, err := cfg.Credentials()
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:   cfg,
		creds: creds,
		// A snapshot bundle can be large and a laggy endpoint should fail rather
		// than hang a capture forever; the per-call context is what actually
		// bounds each request, and this is the backstop for one that has none.
		http: &http.Client{Timeout: 30 * time.Minute},
		now:  time.Now,
	}, nil
}

// Key renders the full object key for a name, under the configured prefix.
func (c *Client) Key(name string) string {
	prefix := strings.Trim(c.cfg.Prefix, "/")
	if prefix == "" {
		return strings.TrimPrefix(name, "/")
	}
	return prefix + "/" + strings.TrimPrefix(name, "/")
}

// Bucket is the configured bucket name, for messages.
func (c *Client) Bucket() string { return c.cfg.Bucket }

// endpointURL builds the request URL for a key.
func (c *Client) endpointURL(key string) (*url.URL, error) {
	host := c.cfg.Endpoint
	scheme := "https"
	if host == "" {
		host = "s3." + c.region() + ".amazonaws.com"
	}
	if i := strings.Index(host, "://"); i >= 0 {
		scheme, host = host[:i], host[i+3:]
	}
	host = strings.TrimSuffix(host, "/")

	u := &url.URL{Scheme: scheme, Host: host}
	if c.cfg.PathStyle {
		u.Path = "/" + c.cfg.Bucket + "/" + key
	} else {
		u.Host = c.cfg.Bucket + "." + host
		u.Path = "/" + key
	}
	// Round-tripping through Parse is what applies net/url's escaping to a path
	// built by concatenation; a key with a space signs and dials correctly only
	// after this.
	return url.Parse(u.Scheme + "://" + u.Host + escapePath(u.Path))
}

// escapePath percent-encodes each segment, leaving separators alone. url.PathEscape
// would escape the slashes; url.URL.String() would leave characters SigV4 expects
// encoded.
func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		parts[i] = url.PathEscape(seg)
	}
	return strings.Join(parts, "/")
}

func (c *Client) region() string { return orDefault(c.cfg.Region, DefaultRegion) }

// do signs and sends one request, returning the response for a 2xx and a
// described error otherwise.
func (c *Client) do(req *http.Request, payloadHash string) (*http.Response, error) {
	sign(req, c.creds, c.region(), payloadHash, c.now())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, req.URL.Path)
	}
	return nil, describe(resp, req)
}

// s3Error is the XML body S3 returns for a failure. Reading it is what turns a
// bare "403 Forbidden" into "SignatureDoesNotMatch" — the difference between a
// wrong key and a wrong clock, which is otherwise a morning of guessing.
type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func describe(resp *http.Response, req *http.Request) error {
	// Bounded: an endpoint that is not S3 at all answers with a whole HTML page,
	// and that page must not become the error message.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	var e s3Error
	if err := xml.Unmarshal(body, &e); err == nil && e.Code != "" {
		return fmt.Errorf("s3: %s %s: %s: %s", req.Method, req.URL.Host, e.Code, e.Message)
	}
	return fmt.Errorf("s3: %s %s: %s", req.Method, req.URL.Host, resp.Status)
}

// Put uploads a file, signing the digest of what is on disk.
//
// Hashing the file before sending it costs one extra read and buys the property
// that a truncated or corrupted upload is rejected by the server as a signature
// mismatch rather than stored as a valid object with the wrong bytes — which for
// a snapshot is the failure that is not discovered until somebody tries to roll
// back.
//
// Single PUT, no multipart: S3 caps that at 5 GiB, which MaxObjectBytes refuses
// well below with a message that names the limit rather than letting the server
// answer with EntityTooLarge.
func (c *Client) Put(ctx context.Context, key, file, contentType string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	u, err := c.endpointURL(c.Key(key))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), f)
	if err != nil {
		return err
	}
	req.ContentLength = st.Size()
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.do(req, hex.EncodeToString(h.Sum(nil)))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Get downloads an object to a file.
func (c *Client) Get(ctx context.Context, key, dest string) error {
	u, err := c.endpointURL(c.Key(key))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, emptyPayload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Written beside the destination and renamed, so an interrupted download
	// cannot leave a half-file that looks like a bundle: git would fail on it
	// later, at the moment somebody is restoring and least wants a puzzle.
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// Stat reports an object's size, or ErrNotFound.
//
// A HEAD, so a failure comes back as a bare status: HTTP forbids a body on a
// HEAD response, which means describe() has no XML error code to read here and
// "403 Forbidden" is genuinely all there is. Every other operation is a GET or a
// PUT and gets the code. Worth knowing before chasing a missing message.
func (c *Client) Stat(ctx context.Context, key string) (int64, error) {
	u, err := c.endpointURL(c.Key(key))
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.do(req, emptyPayload)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	n, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return n, nil
}

// Delete removes an object. S3 answers 204 for a key that was never there, which
// is what makes this safe to call while pruning something already gone.
func (c *Client) Delete(ctx context.Context, key string) error {
	u, err := c.endpointURL(c.Key(key))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, emptyPayload)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if resp != nil {
		resp.Body.Close()
	}
	return nil
}

// Object is one row of a listing.
type Object struct {
	Key      string
	Size     int64
	Modified time.Time
}

type listResult struct {
	Contents []struct {
		Key          string    `xml:"Key"`
		Size         int64     `xml:"Size"`
		LastModified time.Time `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

// List returns every object under a prefix, following continuation tokens.
//
// Paged to exhaustion rather than bounded, because a client that stopped at a
// page would answer "what is in this bucket" with a first page and no way to
// tell that is what it did. Bounding is the caller's decision and belongs where
// the cost is known — rescue.RemoteSessions caps the manifests it reads back and
// reports the number it found, which is the shape a listing owes its reader.
func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	token := ""
	for {
		u, err := c.endpointURL("")
		if err != nil {
			return nil, err
		}
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", c.Key(prefix))
		if token != "" {
			q.Set("continuation-token", token)
		}
		// Encode() sorts by key, which is what SigV4 requires of the canonical
		// query string; building it by hand is how that ordering gets lost.
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.do(req, emptyPayload)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var res listResult
		if err := xml.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("s3: unreadable listing from %s: %w", req.URL.Host, err)
		}
		for _, o := range res.Contents {
			out = append(out, Object{Key: o.Key, Size: o.Size, Modified: o.LastModified})
		}
		if !res.IsTruncated || res.NextContinuationToken == "" {
			return out, nil
		}
		token = res.NextContinuationToken
	}
}

// Check verifies that the bucket answers and that the credential may write to
// it, by listing one key's worth of it.
//
// A list rather than a put: a probe that wrote would leave litter in somebody's
// bucket on every press of a Test button, and a credential allowed to list but
// not to write fails later with a message that names the operation — which is
// more useful than this endpoint guessing about IAM.
func (c *Client) Check(ctx context.Context) error {
	u, err := c.endpointURL("")
	if err != nil {
		return err
	}
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	if p := strings.Trim(c.cfg.Prefix, "/"); p != "" {
		q.Set("prefix", p+"/")
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.do(req, emptyPayload)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// JoinKey builds an object key from segments, which is path.Join with the one
// difference that matters: it never produces a leading slash and never cleans a
// segment away to "." for an empty one.
func JoinKey(parts ...string) string {
	kept := parts[:0]
	for _, p := range parts {
		if p = strings.Trim(p, "/"); p != "" {
			kept = append(kept, p)
		}
	}
	return path.Join(kept...)
}
