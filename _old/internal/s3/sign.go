// Package s3 is the smallest S3 client that can hold a snapshot: put an object,
// get it back, ask whether it is there, delete it, and list what is under a
// prefix.
//
// Hand-rolled rather than imported, for the reason the module has no
// dependencies but cobra and yaml.v3: the AWS SDK is ~15MB of transitive code
// and a release cadence to track, in exchange for five requests whose signing
// algorithm is public, stable since 2012, and about a hundred lines. The cost is
// real and worth naming — no IMDS, no SSO, no config-file profiles, no
// multipart — and it is why credentials are resolved from named environment
// variables and nothing else (see Config.Credentials).
//
// It talks to anything that speaks S3: AWS, MinIO, Cloudflare R2, Backblaze B2,
// Ceph. Endpoint and path-style addressing are configuration rather than a
// vendor list, because a list of other people's products can be neither
// completed nor kept current — the same reason internal/creds keeps its prefix
// table short.
package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// unsignedPayload is the SHA-256 placeholder for a body that is not hashed up
// front.
//
// Every request this package makes signs a real digest instead — a snapshot
// bundle is a file on disk, so hashing it costs one read of something we are
// about to read anyway, and a signed digest is what makes a corrupted upload a
// signature failure rather than a corrupted object. The constant exists because
// some S3-compatible servers reject the header being absent altogether.
const emptyPayload = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

const (
	algorithm = "AWS4-HMAC-SHA256"
	service   = "s3"
)

// sign applies SigV4 to req in place.
//
// The three things easy to get wrong here, all of them silent — a bad signature
// comes back as 403 with no hint about which part was wrong:
//
//   - the canonical URI must be the *escaped* path with "/" left unescaped, and
//     for S3 (unlike every other AWS service) it is not normalized a second
//     time, which is what lets a key contain "..";
//   - every signed header must be lowercased, sorted, and its value trimmed;
//   - Host must be in the signed set. It is not in req.Header — net/http keeps
//     it on the struct — so it is added explicitly.
func sign(req *http.Request, creds Credentials, region, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if creds.SessionToken != "" {
		// A temporary credential's token is part of the signed request, not a
		// side channel: omitting it here yields a signature over headers the
		// server did not receive.
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	signed, canonicalHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonicalHeaders,
		signed,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		hex.EncodeToString(sha256sum([]byte(canonicalRequest))),
	}, "\n")

	key := signingKey(creds.SecretAccessKey, dateStamp, region)
	sig := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, scope, signed, sig))
}

// canonicalURI escapes a path for signing. net/url has already escaped it once;
// what is left is that S3 signs "/" literally and expects an empty path to sign
// as "/".
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	return path
}

// canonicalHeaders returns the signed-header list and the canonical block.
//
// Host comes from req.Host/req.URL rather than req.Header, because net/http does
// not put it in the map — a signature that omits it is rejected by every server
// and by nothing local.
func canonicalHeaders(req *http.Request) (signed, canonical string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values := map[string]string{"host": host}
	for k, v := range req.Header {
		lower := strings.ToLower(k)
		if lower == "authorization" || lower == "user-agent" || lower == "content-length" {
			// Not signed: Authorization is the output, and the other two are
			// rewritten by transports and proxies between here and the server.
			continue
		}
		values[lower] = strings.TrimSpace(strings.Join(v, ","))
	}
	names := make([]string, 0, len(values))
	for k := range values {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(values[n])
		b.WriteByte('\n')
	}
	return strings.Join(names, ";"), b.String()
}

// signingKey derives the date/region/service-scoped key. Deriving it per request
// rather than caching it is deliberate: the cache would have to be invalidated at
// UTC midnight, and this is four HMACs against uploads measured in megabytes.
func signingKey(secret, dateStamp, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(dateStamp))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}
