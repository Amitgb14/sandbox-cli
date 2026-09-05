package s3

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The published AWS worked example for "Transfer Payload in a Single Chunk",
// signed here and compared to the signature AWS printed for it.
//
// A known-answer test rather than a round trip against our own code, and that is
// the whole point of it: every internally consistent signer agrees with itself.
// The only thing that establishes this one is right is a signature somebody else
// computed, and a wrong one is invisible until a real bucket answers 403 with no
// hint about which of six inputs was malformed.
func TestSignatureMatchesTheAWSWorkedExample(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-9")

	creds := Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}
	at := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	sign(req, creds, "us-east-1", emptyPayload, at)

	const want = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	auth := req.Header.Get("Authorization")
	if !strings.Contains(auth, "Signature="+want) {
		t.Errorf("signature does not match the published example\n got: %s\nwant Signature=%s", auth, want)
	}
	if !strings.Contains(auth, "SignedHeaders=host;range;x-amz-content-sha256;x-amz-date") {
		t.Errorf("signed header set differs from the example: %s", auth)
	}
	if !strings.Contains(auth, "Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request") {
		t.Errorf("credential scope differs from the example: %s", auth)
	}
}

// Host is not in http.Request.Header — net/http keeps it on the struct — so a
// signer that walks the header map alone omits it, produces a signature every
// server rejects, and fails nothing locally.
func TestHostIsSignedEvenThoughItIsNotInTheHeaderMap(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://bucket.example.com/k", nil)
	signed, canonical := canonicalHeaders(req)
	if !strings.HasPrefix(signed, "host") {
		t.Fatalf("host missing from the signed set: %q", signed)
	}
	if !strings.Contains(canonical, "host:bucket.example.com\n") {
		t.Fatalf("host missing from the canonical block: %q", canonical)
	}
}

// A temporary credential's token is part of the signed request. Omitted, the
// signature covers headers the server did not receive and every request with an
// STS credential fails.
func TestSessionTokenIsSentAndSigned(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://b.example.com/k", nil)
	sign(req, Credentials{AccessKeyID: "id", SecretAccessKey: "secret", SessionToken: "tok"},
		"us-east-1", emptyPayload, time.Unix(0, 0).UTC())

	if got := req.Header.Get("X-Amz-Security-Token"); got != "tok" {
		t.Fatalf("session token not sent: %q", got)
	}
	if !strings.Contains(req.Header.Get("Authorization"), "x-amz-security-token") {
		t.Fatalf("session token sent but not signed: %s", req.Header.Get("Authorization"))
	}
}

// Content-Length and User-Agent are rewritten by transports and proxies between
// here and the server, so signing them produces failures that depend on the
// network path rather than on the request.
func TestVolatileHeadersAreNotSigned(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPut, "https://b.example.com/k", strings.NewReader("x"))
	req.Header.Set("User-Agent", "sandbox-cli")
	req.Header.Set("Content-Length", "1")
	signed, _ := canonicalHeaders(req)
	for _, name := range []string{"user-agent", "content-length"} {
		if strings.Contains(signed, name) {
			t.Errorf("%s must not be signed, got %q", name, signed)
		}
	}
}
