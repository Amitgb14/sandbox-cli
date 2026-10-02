package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestOf(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

// registry is a minimal registry: one repository, token auth, an index with two
// platforms. tamper lets a test corrupt what it serves.
type registry struct {
	blobs     map[string][]byte
	manifests map[string][]byte // reference -> body
	types     map[string]string
	tamper    func(path string, body []byte) []byte
	tokens    int
}

func newRegistry(t *testing.T, arch string) (*registry, string, string) {
	t.Helper()
	r := &registry{blobs: map[string][]byte{}, manifests: map[string][]byte{}, types: map[string]string{}}
	cfg := []byte(`{"config":{"Env":["PATH=/opt/bin:/usr/bin","FOO=bar"],"WorkingDir":"/app"}}`)
	lay := layer(t, true, dir("etc"), file("etc/release", "fake 1.0\n")).Bytes()
	r.blobs[digestOf(cfg)] = cfg
	r.blobs[digestOf(lay)] = lay
	man, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": mtOCIManifest,
		"config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": digestOf(cfg), "size": len(cfg)},
		"layers": []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar+gzip", "digest": digestOf(lay), "size": len(lay)}},
	})
	md := digestOf(man)
	r.manifests[md], r.types[md] = man, mtOCIManifest
	idx, _ := json.Marshal(map[string]any{
		"schemaVersion": 2, "mediaType": mtOCIIndex,
		"manifests": []any{
			map[string]any{"mediaType": mtOCIManifest, "digest": "sha256:" + strings.Repeat("0", 64), "size": 1, "platform": map[string]string{"os": "linux", "architecture": "other"}},
			map[string]any{"mediaType": mtOCIManifest, "digest": md, "size": len(man), "platform": map[string]string{"os": "linux", "architecture": arch}},
		},
	})
	r.manifests["1.0"], r.types["1.0"] = idx, mtOCIIndex
	r.manifests[digestOf(idx)], r.types[digestOf(idx)] = idx, mtOCIIndex
	return r, md, digestOf(idx)
}

func (r *registry) handler(host *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/token" {
			r.tokens++
			json.NewEncoder(w).Encode(map[string]string{"token": "t0k"})
			return
		}
		if req.Header.Get("Authorization") != "Bearer t0k" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://`+*host+`/token",service="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body []byte
		if ref, ok := strings.CutPrefix(req.URL.Path, "/v2/team/app/manifests/"); ok {
			body = r.manifests[ref]
			w.Header().Set("Content-Type", r.types[ref])
		} else if d, ok := strings.CutPrefix(req.URL.Path, "/v2/team/app/blobs/"); ok {
			body = r.blobs[d]
		}
		if body == nil {
			http.NotFound(w, req)
			return
		}
		if r.tamper != nil {
			body = r.tamper(req.URL.Path, body)
		}
		w.Write(body)
	})
}

func puller(t *testing.T, r *registry) (*Puller, string) {
	t.Helper()
	var host string
	srv := httptest.NewServer(r.handler(&host))
	t.Cleanup(srv.Close)
	host = strings.TrimPrefix(srv.URL, "http://")
	return &Puller{Cache: t.TempDir(), PlainHTTP: map[string]bool{host: true}, Arch: "amd64"}, host
}

func TestParseRef(t *testing.T) {
	for in, want := range map[string]Ref{
		"alpine":                        {Registry: "registry-1.docker.io", Repo: "library/alpine", Tag: "latest"},
		"alpine:3.20":                   {Registry: "registry-1.docker.io", Repo: "library/alpine", Tag: "3.20"},
		"ghcr.io/team/app:1":            {Registry: "ghcr.io", Repo: "team/app", Tag: "1"},
		"localhost:5000/app":            {Registry: "localhost:5000", Repo: "app", Tag: "latest"},
		"team/app@sha256:" + hex64("a"): {Registry: "registry-1.docker.io", Repo: "team/app", Digest: "sha256:" + hex64("a")},
	} {
		got, err := ParseRef(in)
		if err != nil || got != want {
			t.Errorf("ParseRef(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"UPPER/app", "app:bad tag", "app@sha256:short", "--privileged", "a//b"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
}

func hex64(c string) string { return strings.Repeat(c, 64) }

func TestPullResolvesThePlatformAndVerifies(t *testing.T) {
	r, manDigest, _ := newRegistry(t, "amd64")
	p, host := puller(t, r)
	got, err := p.Pull(context.Background(), host+"/team/app:1.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != manDigest {
		t.Errorf("digest = %s, want the platform manifest %s", got.Digest, manDigest)
	}
	if len(got.Layers) != 1 || got.Config.WorkingDir != "/app" || got.Config.Env[1] != "FOO=bar" {
		t.Errorf("pulled: %+v", got)
	}
	// Cached: a second pull downloads no blob.
	before := r.tokens
	r.blobs = map[string][]byte{}
	if _, err := p.Pull(context.Background(), host+"/team/app:1.0"); err != nil {
		t.Fatalf("second pull needed the network for blobs: %v", err)
	}
	_ = before
}

func TestPullRefusesTamperedContent(t *testing.T) {
	cases := map[string]func(path string, b []byte) []byte{
		"a flipped byte in a layer": func(p string, b []byte) []byte {
			if strings.Contains(p, "/blobs/") && len(b) > 100 {
				c := bytes.Clone(b)
				c[50] ^= 0xff
				return c
			}
			return b
		},
		"a layer longer than declared": func(p string, b []byte) []byte {
			if strings.Contains(p, "/blobs/") && len(b) > 100 {
				return append(bytes.Clone(b), 0)
			}
			return b
		},
		"a platform manifest that is not the one indexed": func(p string, b []byte) []byte {
			if strings.Contains(p, "/manifests/sha256:") && !strings.Contains(string(b), "manifests") {
				return append(bytes.Clone(b), ' ')
			}
			return b
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, _ := newRegistry(t, "amd64")
			r.tamper = tamper
			p, host := puller(t, r)
			if _, err := p.Pull(context.Background(), host+"/team/app:1.0"); err == nil {
				t.Fatal("tampered content was accepted")
			}
			des, _ := os.ReadDir(filepath.Join(p.Cache, "blobs", "sha256"))
			for _, d := range des {
				if !strings.HasPrefix(d.Name(), ".partial") {
					b, _ := os.ReadFile(filepath.Join(p.Cache, "blobs", "sha256", d.Name()))
					if "sha256:"+d.Name() != digestOf(b) {
						t.Fatalf("a blob was cached under a digest it does not have: %s", d.Name())
					}
				}
			}
		})
	}
}

func TestPullByDigestMustMatch(t *testing.T) {
	r, _, idxDigest := newRegistry(t, "amd64")
	p, host := puller(t, r)
	if _, err := p.Pull(context.Background(), host+"/team/app@"+idxDigest); err != nil {
		t.Fatalf("pull by the right digest: %v", err)
	}
	wrong := "sha256:" + hex64("b")
	r.manifests[wrong], r.types[wrong] = r.manifests["1.0"], mtOCIIndex
	if _, err := p.Pull(context.Background(), host+"/team/app@"+wrong); err == nil {
		t.Fatal("content served under a digest it does not hash to was accepted")
	}
}

func TestPullRefusesAMissingPlatform(t *testing.T) {
	r, _, _ := newRegistry(t, "arm64")
	p, host := puller(t, r)
	if _, err := p.Pull(context.Background(), host+"/team/app:1.0"); err == nil || !strings.Contains(err.Error(), "no image for linux/amd64") {
		t.Fatalf("err = %v", err)
	}
}

// The whole Linux path without a VM: pull, unpack, inject, mkfs.
func TestBuildRootFS(t *testing.T) {
	if _, err := os.Stat("/usr/sbin/mkfs.ext4"); err != nil {
		if _, err2 := os.Stat("/sbin/mkfs.ext4"); err2 != nil {
			t.Skip("mkfs.ext4 not available")
		}
	}
	r, _, _ := newRegistry(t, "amd64")
	p, host := puller(t, r)
	agent := filepath.Join(t.TempDir(), "agent")
	os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755)
	dir := t.TempDir()
	fs1, err := BuildRootFS(context.Background(), p, host+"/team/app:1.0", agent, dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(fs1.Path); err != nil || fi.Size() == 0 {
		t.Fatalf("no disk: %v", err)
	}
	fs2, err := BuildRootFS(context.Background(), p, host+"/team/app:1.0", agent, dir)
	if err != nil || fs2.Path != fs1.Path {
		t.Fatalf("not reused: %v %s", err, fs2.Path)
	}
	os.WriteFile(agent, []byte("#!/bin/sh\n# changed\n"), 0o755)
	fs3, err := BuildRootFS(context.Background(), p, host+"/team/app:1.0", agent, dir)
	if err != nil || fs3.Path == fs1.Path {
		t.Fatalf("a changed guest agent reused the old disk: %v", err)
	}
}

// Offline, a pull falls back to the manifest last fetched for the reference —
// the blobs are already cached and were verified when they arrived.
func TestPullFallsBackToTheCachedManifestWhenOffline(t *testing.T) {
	r, digest, _ := newRegistry(t, "amd64")
	var host string
	srv := httptest.NewServer(r.handler(&host))
	host = strings.TrimPrefix(srv.URL, "http://")
	p := &Puller{Cache: t.TempDir(), PlainHTTP: map[string]bool{host: true}, Arch: "amd64"}
	if _, err := p.Pull(context.Background(), host+"/team/app:1.0"); err != nil {
		t.Fatal(err)
	}
	srv.Close() // the registry is gone
	got, err := p.Pull(context.Background(), host+"/team/app:1.0")
	if err != nil {
		t.Fatalf("offline pull: %v", err)
	}
	if got.Digest != digest {
		t.Errorf("offline digest %s, want %s", got.Digest, digest)
	}
	// A reference never fetched has nothing to fall back to.
	if _, err := p.Pull(context.Background(), host+"/team/app:2.0"); err == nil {
		t.Error("an unseen reference resolved offline")
	}
}
