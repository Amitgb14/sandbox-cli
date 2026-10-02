package egressproxy

import (
	"bytes"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMatcherDoesNotImplySubdomains is the point of the whole package. The
// firewall this replaces matched resolved addresses, so allowing github.com also
// allowed gist.github.com — which shares its address and is a *write* endpoint,
// i.e. an exfiltration channel nobody listed. Breadth has to be asked for.
func TestMatcherDoesNotImplySubdomains(t *testing.T) {
	m := NewMatcher([]string{"github.com", "api.anthropic.com"})

	for _, h := range []string{"github.com", "api.anthropic.com", "GitHub.com", "github.com."} {
		if !m.Allows(h) {
			t.Errorf("Allows(%q) = false, want true", h)
		}
	}
	for _, h := range []string{
		"gist.github.com",     // the confirmed exfiltration channel
		"api.github.com",      // a subdomain is not the domain
		"evilgithub.com",      // suffix without a label boundary
		"github.com.evil.net", // the allowlisted name as a prefix
		"notgithub.com",
		"", " ",
	} {
		if m.Allows(h) {
			t.Errorf("Allows(%q) = true, want false", h)
		}
	}
}

// TestMatcherWildcardNeedsALabelBoundary covers the one explicit form of
// breadth. "*.example.com" must not match example.com itself (that is a
// different host, and often a different service), and must not match a name that
// merely ends in the same letters.
func TestMatcherWildcardNeedsALabelBoundary(t *testing.T) {
	m := NewMatcher([]string{"*.example.com"})

	for _, h := range []string{"api.example.com", "a.b.example.com", "API.Example.Com"} {
		if !m.Allows(h) {
			t.Errorf("Allows(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"example.com", "evil-example.com", "notexample.com", "com"} {
		if m.Allows(h) {
			t.Errorf("Allows(%q) = true, want false", h)
		}
	}
}

// TestMatcherNormalisation pins that one host cannot be spelled two ways to get
// two answers. DNS is case-insensitive, a trailing dot is the same name written
// absolutely, and a port is not part of the name.
func TestMatcherNormalisation(t *testing.T) {
	m := NewMatcher([]string{"  GitHub.COM.  ", "", "   "})
	if m.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (blank patterns ignored)", m.Len())
	}
	for _, h := range []string{"github.com", "GITHUB.COM", "github.com.", "github.com:443"} {
		if !m.Allows(h) {
			t.Errorf("Allows(%q) = false; one host must not be spellable into two answers", h)
		}
	}
}

// TestSNIFromRealClientHello uses a handshake produced by crypto/tls rather than
// a hand-written fixture, so the parser is checked against what a real client
// actually sends.
func TestSNIFromRealClientHello(t *testing.T) {
	hello := captureClientHello(t, "api.anthropic.com")
	got, err := SNIFromClientHello(hello)
	if err != nil {
		t.Fatalf("SNIFromClientHello: %v", err)
	}
	if got != "api.anthropic.com" {
		t.Errorf("SNI = %q, want api.anthropic.com", got)
	}
}

// TestSNIAbsentIsRefusedNotGuessed pins the decision that keeps the allowlist
// meaningful: a connection with no name is an error, not a fallback to the
// destination address. Falling back would permit "connect straight to the IP",
// which is the most obvious way to try to evade a name-based allowlist.
func TestSNIAbsentIsRefusedNotGuessed(t *testing.T) {
	// A handshake to a bare IP carries no SNI.
	hello := captureClientHello(t, "")
	_, err := SNIFromClientHello(hello)
	if !errors.Is(err, ErrNoHostname) {
		t.Fatalf("err = %v, want ErrNoHostname", err)
	}
}

// TestSNIParserSurvivesHostileInput is the reason this parser is in Go with
// tests rather than in a shell script. Every byte comes from the agent, so a
// lying length field or a truncated record must end the parse, never slice past
// the buffer or spin.
func TestSNIParserSurvivesHostileInput(t *testing.T) {
	valid := captureClientHello(t, "example.com")

	cases := map[string][]byte{
		"empty":            {},
		"not a handshake":  {0x17, 0x03, 0x03, 0x00, 0x05, 1, 2, 3, 4, 5},
		"header only":      {0x16, 0x03, 0x01, 0x00, 0x00},
		"absurd rec len":   {0x16, 0x03, 0x01, 0xff, 0xff},
		"truncated body":   valid[:len(valid)/2],
		"one byte":         valid[:1],
		"five bytes":       valid[:5],
		"all zeroes":       make([]byte, 300),
		"length overreach": {0x16, 0x03, 0x01, 0x00, 0x10, 0x01, 0xff, 0xff, 0xff},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			// The contract is only "returns, does not panic, does not invent a host".
			host, err := SNIFromClientHello(in)
			if err == nil && host == "" {
				t.Error("returned no error and no host; one of the two must be true")
			}
			if err == nil && host != "" {
				t.Errorf("invented a hostname %q from malformed input", host)
			}
		})
	}

	// Every truncation of a real hello must also be safe — this is where an
	// off-by-one in a length check shows up.
	for i := 0; i < len(valid); i++ {
		if _, err := SNIFromClientHello(valid[:i]); err == nil {
			t.Errorf("truncation at %d parsed as valid", i)
		}
	}
}

// captureClientHello produces a genuine ClientHello for serverName by starting a
// handshake against a pipe and reading what crypto/tls writes.
func captureClientHello(t *testing.T, serverName string) []byte {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4096)
		n, _ := server.Read(buf)
		done <- append([]byte(nil), buf[:n]...)
		server.Close()
	}()
	cfg := &tls.Config{InsecureSkipVerify: true}
	if serverName != "" {
		cfg.ServerName = serverName
	}
	go tls.Client(client, cfg).Handshake()
	hello := <-done
	client.Close()
	if len(hello) < 10 {
		t.Fatalf("captured only %d bytes; not a ClientHello", len(hello))
	}
	return hello
}

// FuzzSNIFromClientHello guards the parser the way a table of cases cannot. It
// reads bytes an agent chooses, so the property under test is simply that no
// input makes it panic, hang, or return a hostname it did not actually find.
//
// Seeded with a real handshake so the fuzzer starts from a shape that reaches the
// interesting code rather than bouncing off the first length check.
func FuzzSNIFromClientHello(f *testing.F) {
	f.Add(captureClientHello(&testing.T{}, "example.com"))
	f.Add([]byte{0x16, 0x03, 0x01, 0x00, 0x00})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		host, err := SNIFromClientHello(data)
		if err != nil {
			return
		}
		if host == "" {
			t.Fatal("returned a nil error with an empty hostname")
		}
		// A hostname it claims to have found must actually appear in the input;
		// anything else means the parser is synthesising a name, which would be
		// a name the allowlist then decides on.
		if !bytes.Contains(bytes.ToLower(data), []byte(host)) {
			t.Fatalf("returned %q, which is not present in the input", host)
		}
	})
}

// TestEmbeddedSourcesAreComplete fails when a source file is added to this
// package and not added to the //go:embed list.
//
// Without it, a new file would simply be absent from the image build context and
// the proxy would fail to compile — inside a docker build, long after the change
// that caused it, with an error pointing at the wrong place. Test files are
// deliberately excluded and are checked to stay excluded.
func TestEmbeddedSourcesAreComplete(t *testing.T) {
	onDisk, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, f := range onDisk {
		if strings.HasSuffix(f, "_test.go") || f == "embed.go" {
			continue // tests never ship; embed.go is the shipper, not the shipped
		}
		want[f] = true
	}

	got := map[string]bool{}
	for _, f := range EmbeddedFiles() {
		got[f] = true
		if strings.HasSuffix(f, "_test.go") {
			t.Errorf("%s is a test file and must not ship in the image", f)
		}
	}
	for f := range want {
		if !got[f] {
			t.Errorf("%s is not in the //go:embed list in embed.go, so it would be missing "+
				"from the image build context and the proxy would fail to compile", f)
		}
	}
	for f := range got {
		if !want[f] {
			t.Errorf("%s is embedded but no longer exists", f)
		}
	}
}

// TestEmbeddedSourcesCompile builds the generated build context with the real
// toolchain, so a syntax error in the generated main or a missing import is
// caught here rather than inside a docker build.
func TestEmbeddedSourcesCompile(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	dir := t.TempDir()
	err := WriteBuildContext(dir, func(name string, data []byte) error {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "proxy-bin"), ".")
	cmd.Dir = filepath.Join(dir, "proxy")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the embedded proxy source does not compile for linux:\n%s", out)
	}
}
