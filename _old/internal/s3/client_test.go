package s3

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testClient points a client at a local server, which requires path-style
// addressing: the default puts the bucket in the hostname, and no test server
// answers to "bucket.127.0.0.1".
func testClient(t *testing.T, cfg Config, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	cfg.Endpoint = srv.URL
	cfg.PathStyle = true
	if cfg.Bucket == "" {
		cfg.Bucket = "b"
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func TestCredentialsComeFromTheNamedVariables(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")
	t.Setenv("MY_KEY", "id")
	t.Setenv("MY_SECRET", "secret")

	cfg := Config{Bucket: "b", AccessKeyEnv: "MY_KEY", SecretKeyEnv: "MY_SECRET"}
	creds, err := cfg.Credentials()
	if err != nil {
		t.Fatalf("named variables were set but not read: %v", err)
	}
	if creds.AccessKeyID != "id" || creds.SecretAccessKey != "secret" {
		t.Fatalf("wrong credential read: %+v", creds)
	}
}

// Half a credential is a misconfiguration, not an absence, and the message has
// to name the variable that is missing — "no credentials" for an account whose
// key *is* exported sends people to the wrong file.
func TestHalfACredentialNamesTheMissingVariable(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "")

	_, err := Config{Bucket: "b"}.Credentials()
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("want ErrNoCredentials, got %v", err)
	}
	if !strings.Contains(err.Error(), "AWS_SECRET_ACCESS_KEY") {
		t.Fatalf("the message must name the variable that is missing: %v", err)
	}
}

// The two addressing styles produce different URLs, and getting it wrong is a
// connection to a hostname that does not exist (vhost against MinIO) or a 404
// with no explanation (path-style against a bucket that expects vhost).
func TestAddressingStyle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       Config
		wantHost  string
		wantPath  string
		pathStyle bool
	}{
		{
			name:     "aws virtual host",
			cfg:      Config{Bucket: "mybucket", Region: "eu-west-1"},
			wantHost: "mybucket.s3.eu-west-1.amazonaws.com",
			wantPath: "/snapshots/x.bundle",
		},
		{
			name:     "self-hosted path style",
			cfg:      Config{Bucket: "mybucket", Endpoint: "http://minio.local:9000", PathStyle: true},
			wantHost: "minio.local:9000",
			wantPath: "/mybucket/snapshots/x.bundle",
		},
		{
			name:     "endpoint without a scheme defaults to https",
			cfg:      Config{Bucket: "mybucket", Endpoint: "s3.example.com", PathStyle: true},
			wantHost: "s3.example.com",
			wantPath: "/mybucket/snapshots/x.bundle",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{cfg: tc.cfg}
			u, err := c.endpointURL(c.Key("snapshots/x.bundle"))
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != tc.wantHost {
				t.Errorf("host = %q, want %q", u.Host, tc.wantHost)
			}
			if u.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", u.Path, tc.wantPath)
			}
		})
	}
}

func TestPrefixNamespacesEveryKey(t *testing.T) {
	c := &Client{cfg: Config{Bucket: "b", Prefix: "/team-a/"}}
	if got := c.Key("snapshots/x.bundle"); got != "team-a/snapshots/x.bundle" {
		t.Fatalf("Key = %q, want the prefix applied with slashes normalised", got)
	}
	bare := &Client{cfg: Config{Bucket: "b"}}
	if got := bare.Key("/snapshots/x"); got != "snapshots/x" {
		t.Fatalf("Key = %q, want no leading slash", got)
	}
}

// A PUT signs the digest of what is on disk, so a body that changed underneath
// is a signature failure rather than a stored object with the wrong bytes.
func TestPutSignsTheFileDigestAndSendsTheBody(t *testing.T) {
	var gotBody, gotHash, gotPath string
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody, gotHash, gotPath = string(b), r.Header.Get("X-Amz-Content-Sha256"), r.URL.Path
	}))

	file := filepath.Join(t.TempDir(), "x.bundle")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(context.Background(), "snapshots/x.bundle", file, bundleType); err != nil {
		t.Fatal(err)
	}
	if gotBody != "hello" {
		t.Errorf("body = %q, want the file's contents", gotBody)
	}
	// sha256("hello")
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if gotHash != want {
		t.Errorf("X-Amz-Content-Sha256 = %q, want the digest of the file %q", gotHash, want)
	}
	if gotPath != "/b/snapshots/x.bundle" {
		t.Errorf("path = %q", gotPath)
	}
}

const bundleType = "application/x-git-bundle"

// A truncated download must not be left where a complete one goes: git would
// fail on it later, at the moment somebody is restoring and least wants a
// puzzle.
func TestGetWritesThroughATempFile(t *testing.T) {
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "bundle-bytes")
	}))
	dest := filepath.Join(t.TempDir(), "out.bundle")
	if err := c.Get(context.Background(), "snapshots/x.bundle", dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "bundle-bytes" {
		t.Fatalf("got %q", b)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("the .part file survived the rename")
	}
}

// 404 is its own error because pruning and fetching branch on it: a key that was
// never there is fine to delete and fatal to restore from.
func TestNotFoundIsDistinguishable(t *testing.T) {
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	_, err := c.Stat(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// A delete of an absent key is not a failure; pruning a snapshot whose
	// upload died halfway must not error.
	if err := c.Delete(context.Background(), "missing"); err != nil {
		t.Fatalf("deleting an absent key: %v", err)
	}
}

// "403 Forbidden" alone is a morning of guessing. The XML body says whether the
// key is wrong or the clock is.
//
// Asserted through Check rather than Stat, and the reason is a property of HTTP
// rather than of this test: Stat is a HEAD, whose response may carry no body, so
// there is no error document to read there however the server is feeling. See
// Stat's own comment.
func TestErrorBodyIsReadForTheS3Code(t *testing.T) {
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<Error><Code>SignatureDoesNotMatch</Code><Message>The request signature we calculated does not match</Message></Error>`)
	}))
	err := c.Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("want the S3 error code in the message, got %v", err)
	}
}

// A bare status is all a HEAD can ever produce, so a caller reading Stat's error
// for a code will not find one. Pinned so nobody spends an afternoon on it.
func TestStatCannotCarryAnErrorCode(t *testing.T) {
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `<Error><Code>AccessDenied</Code></Error>`)
	}))
	_, err := c.Stat(context.Background(), "k")
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("want the status in the message, got %v", err)
	}
}

// Pruning stops at a page boundary if continuation tokens are ignored, and
// objects left behind forever are noticed last, on a bill.
func TestListFollowsContinuationTokens(t *testing.T) {
	var pages int
	c, _ := testClient(t, Config{Bucket: "b"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("continuation-token") == "" {
			fmt.Fprint(w, `<ListBucketResult><Contents><Key>a</Key><Size>1</Size></Contents>`+
				`<IsTruncated>true</IsTruncated><NextContinuationToken>t2</NextContinuationToken></ListBucketResult>`)
			return
		}
		fmt.Fprint(w, `<ListBucketResult><Contents><Key>b</Key><Size>2</Size></Contents>`+
			`<IsTruncated>false</IsTruncated></ListBucketResult>`)
	}))
	objs, err := c.List(context.Background(), "snapshots")
	if err != nil {
		t.Fatal(err)
	}
	if pages != 2 {
		t.Errorf("followed %d pages, want 2", pages)
	}
	if len(objs) != 2 || objs[0].Key != "a" || objs[1].Key != "b" {
		t.Errorf("objects = %+v, want both pages", objs)
	}
}
