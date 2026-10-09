package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Ref is a parsed image reference.
type Ref struct {
	Registry string // host[:port] the API is served from
	Repo     string // e.g. library/alpine
	Tag      string
	Digest   string // sha256:… when pinned
}

func (r Ref) String() string {
	s := r.Registry + "/" + r.Repo
	if r.Digest != "" {
		return s + "@" + r.Digest
	}
	return s + ":" + r.Tag
}

func (r Ref) reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	return r.Tag
}

var (
	repoRE   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	tagRE    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	digestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

// ParseRef parses an image reference the way the common tools do: a first
// component with a dot, a colon or "localhost" is a registry; otherwise the
// default registry and, for a single name, its library/ namespace.
func ParseRef(s string) (Ref, error) {
	r := Ref{}
	rest := s
	if i := strings.Index(rest, "@"); i >= 0 {
		r.Digest, rest = rest[i+1:], rest[:i]
		if !digestRE.MatchString(r.Digest) {
			return Ref{}, fmt.Errorf("image %q: digest must be sha256:<64 hex>", s)
		}
	}
	if first, after, ok := strings.Cut(rest, "/"); ok && (strings.ContainsAny(first, ".:") || first == "localhost") {
		r.Registry, rest = first, after
	} else {
		r.Registry = "registry-1.docker.io"
		if !strings.Contains(rest, "/") {
			rest = "library/" + rest
		}
	}
	if i := strings.LastIndex(rest, ":"); i >= 0 {
		r.Tag, rest = rest[i+1:], rest[:i]
	}
	if r.Tag == "" && r.Digest == "" {
		r.Tag = "latest"
	}
	r.Repo = rest
	if !repoRE.MatchString(r.Repo) {
		return Ref{}, fmt.Errorf("image %q: repository %q is not valid", s, r.Repo)
	}
	if r.Tag != "" && !tagRE.MatchString(r.Tag) {
		return Ref{}, fmt.Errorf("image %q: tag %q is not valid", s, r.Tag)
	}
	return r, nil
}

// Media types this puller understands.
const (
	mtOCIIndex     = "application/vnd.oci.image.index.v1+json"
	mtDockerList   = "application/vnd.docker.distribution.manifest.list.v2+json"
	mtOCIManifest  = "application/vnd.oci.image.manifest.v1+json"
	mtDockerManif  = "application/vnd.docker.distribution.manifest.v2+json"
	maxManifest    = 4 << 20
	maxLayers      = 128
	defaultMaxBlob = 8 << 30
)

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  *struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
		Variant      string `json:"variant"`
	} `json:"platform,omitempty"`
}

type manifest struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"` // index
	Config    descriptor   `json:"config"`    // manifest
	Layers    []descriptor `json:"layers"`
}

// Config is the part of an image's configuration a sandbox uses.
type Config struct {
	Env        []string `json:"Env"`
	WorkingDir string   `json:"WorkingDir"`
	User       string   `json:"User"`
}

// Pulled is an image whose blobs are in the cache and verified.
type Pulled struct {
	Ref    Ref
	Digest string   // the platform manifest's digest: the image's identity
	Layers []string // cached layer files, base first
	Config Config
	// Blobs are the digests of the image's config and layers: what of the
	// blob cache it needs, so removing it knows what is still shared.
	Blobs []string
}

// Puller fetches images into a content-addressed cache.
type Puller struct {
	Cache string       // directory holding blobs/sha256/<hex>
	HTTP  *http.Client // nil means http.DefaultClient
	// PlainHTTP lists registries reached over http rather than https — a local
	// test registry, never a remote one.
	PlainHTTP map[string]bool
	OS, Arch  string // platform; default linux/runtime.GOARCH
	MaxBlob   int64  // per blob; default 8 GiB
	Logf      func(format string, a ...any)

	mu     sync.Mutex
	tokens map[string]string // repo -> bearer token
}

func (p *Puller) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

func (p *Puller) base(r Ref) string {
	scheme := "https"
	if p.PlainHTTP[r.Registry] {
		scheme = "http"
	}
	return scheme + "://" + r.Registry + "/v2/" + r.Repo
}

// Pull resolves ref for the platform and caches its config and layers.
func (p *Puller) Pull(ctx context.Context, ref string) (*Pulled, error) {
	r, err := ParseRef(ref)
	if err != nil {
		return nil, err
	}
	osName, arch := p.OS, p.Arch
	if osName == "" {
		osName = "linux"
	}
	if arch == "" {
		arch = runtime.GOARCH
	}

	body, mt, digest, err := p.fetchManifest(ctx, r, r.reference())
	if err != nil {
		return nil, err
	}
	if r.Digest != "" && digest != r.Digest {
		return nil, fmt.Errorf("%s: the registry served content with digest %s", ref, digest)
	}
	var m manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("%s: unreadable manifest: %w", ref, err)
	}
	if mt == "" {
		mt = m.MediaType
	}
	if mt == mtOCIIndex || mt == mtDockerList {
		var pick *descriptor
		for i, d := range m.Manifests {
			if d.Platform != nil && d.Platform.OS == osName && d.Platform.Architecture == arch {
				pick = &m.Manifests[i]
				break
			}
		}
		if pick == nil {
			return nil, fmt.Errorf("%s has no image for %s/%s", ref, osName, arch)
		}
		body, mt, digest, err = p.fetchManifest(ctx, r, pick.Digest)
		if err != nil {
			return nil, err
		}
		if digest != pick.Digest {
			return nil, fmt.Errorf("%s: platform manifest digest mismatch", ref)
		}
		m = manifest{}
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, fmt.Errorf("%s: unreadable manifest: %w", ref, err)
		}
		if mt == "" {
			mt = m.MediaType
		}
	}
	if mt != mtOCIManifest && mt != mtDockerManif {
		return nil, fmt.Errorf("%s: unsupported manifest type %q", ref, mt)
	}
	if len(m.Layers) == 0 || len(m.Layers) > maxLayers {
		return nil, fmt.Errorf("%s: %d layers (want 1..%d)", ref, len(m.Layers), maxLayers)
	}

	out := &Pulled{Ref: r, Digest: digest}
	if ps := progressOf(ctx); ps != nil {
		total := m.Config.Size
		for _, l := range m.Layers {
			total += l.Size
		}
		ps.total.Store(total)
		ps.report("pulling")
	}
	out.Blobs = append(out.Blobs, m.Config.Digest)
	cfgPath, err := p.blob(ctx, r, m.Config)
	if err != nil {
		return nil, fmt.Errorf("%s: config: %w", ref, err)
	}
	var cfg struct {
		Config Config `json:"config"`
	}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	out.Config = cfg.Config
	for _, l := range m.Layers {
		if !strings.Contains(l.MediaType, "tar") || strings.Contains(l.MediaType, "zstd") {
			return nil, fmt.Errorf("%s: layer type %q is not supported", ref, l.MediaType)
		}
		lp, err := p.blob(ctx, r, l)
		if err != nil {
			return nil, fmt.Errorf("%s: layer %s: %w", ref, l.Digest, err)
		}
		out.Layers = append(out.Layers, lp)
		out.Blobs = append(out.Blobs, l.Digest)
	}
	return out, nil
}

var accept = strings.Join([]string{mtOCIIndex, mtDockerList, mtOCIManifest, mtDockerManif}, ", ")

func (p *Puller) fetchManifest(ctx context.Context, r Ref, reference string) (body []byte, mediaType, digest string, err error) {
	resp, err := p.get(ctx, r, p.base(r)+"/manifests/"+url.PathEscape(reference), accept)
	if err != nil {
		// Offline — the registry could not be reached at all, as opposed to
		// refusing — falls back to the manifest last fetched for this reference.
		// It was verified when it was fetched; what may be stale is which
		// manifest a mutable tag points at, and the log says so.
		var ne net.Error
		if errors.As(err, &ne) || isDialError(err) {
			if b, mt, ok := p.cachedManifest(r, reference); ok {
				if p.Logf != nil {
					p.Logf("registry %s unreachable; using the manifest cached for %s:%s", r.Registry, r.Repo, reference)
				}
				sum := sha256.Sum256(b)
				return b, mt, "sha256:" + hex.EncodeToString(sum[:]), nil
			}
		}
		return nil, "", "", err
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxManifest+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(body) > maxManifest {
		return nil, "", "", fmt.Errorf("manifest larger than %d bytes", maxManifest)
	}
	sum := sha256.Sum256(body)
	mt, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
	mt = strings.TrimSpace(mt)
	p.cacheManifest(r, reference, body, mt)
	return body, mt, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (p *Puller) manifestPath(r Ref, reference string) string {
	k := sha256.Sum256([]byte(r.Registry + "/" + r.Repo + "@" + reference))
	return filepath.Join(p.Cache, "manifests", hex.EncodeToString(k[:]))
}

func (p *Puller) cacheManifest(r Ref, reference string, body []byte, mt string) {
	path := p.manifestPath(r, reference)
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	b, _ := json.Marshal(struct {
		MediaType string `json:"media_type"`
		Body      []byte `json:"body"`
	}{mt, body})
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func (p *Puller) cachedManifest(r Ref, reference string) ([]byte, string, bool) {
	b, err := os.ReadFile(p.manifestPath(r, reference))
	if err != nil {
		return nil, "", false
	}
	var m struct {
		MediaType string `json:"media_type"`
		Body      []byte `json:"body"`
	}
	if json.Unmarshal(b, &m) != nil || len(m.Body) == 0 {
		return nil, "", false
	}
	return m.Body, m.MediaType, true
}

func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// blob downloads a blob into the cache unless it is already there, verifying
// its digest and that it is exactly the declared size.
func (p *Puller) blob(ctx context.Context, r Ref, d descriptor) (string, error) {
	if !digestRE.MatchString(d.Digest) {
		return "", fmt.Errorf("digest %q is not sha256", d.Digest)
	}
	max := p.MaxBlob
	if max == 0 {
		max = defaultMaxBlob
	}
	if d.Size < 0 || d.Size > max {
		return "", fmt.Errorf("blob of %d bytes is outside 0..%d", d.Size, max)
	}
	dir := filepath.Join(p.Cache, "blobs", "sha256")
	final := filepath.Join(dir, strings.TrimPrefix(d.Digest, "sha256:"))
	if fi, err := os.Stat(final); err == nil && fi.Size() == d.Size {
		if ps := progressOf(ctx); ps != nil {
			ps.done.Add(d.Size)
			ps.report("pulling")
		}
		return final, nil // written only after verification, so its name is its proof
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	resp, err := p.get(ctx, r, p.base(r)+"/blobs/"+d.Digest, "")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	tmp, err := os.CreateTemp(dir, ".partial-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(progressWriter(ctx, io.MultiWriter(tmp, h)), io.LimitReader(resp.Body, d.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	if n != d.Size {
		return "", fmt.Errorf("blob %s: got %d bytes, manifest says %d", d.Digest, n, d.Size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != d.Digest {
		return "", fmt.Errorf("blob %s: content hashes to %s", d.Digest, got)
	}
	return final, os.Rename(tmp.Name(), final)
}

// get performs a GET, answering a bearer-token challenge anonymously once.
func (p *Puller) get(ctx context.Context, r Ref, u, accept string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		p.mu.Lock()
		tok := p.tokens[r.Registry+"/"+r.Repo]
		p.mu.Unlock()
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if err := p.authenticate(ctx, r, resp.Header.Get("WWW-Authenticate")); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	return nil, errors.New("unauthorized after authenticating")
}

var challengeRE = regexp.MustCompile(`(\w+)="([^"]*)"`)

// authenticate fetches an anonymous pull token for the repository.
func (p *Puller) authenticate(ctx context.Context, r Ref, challenge string) error {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return fmt.Errorf("registry %s wants %q, which anonymous pulls cannot answer", r.Registry, challenge)
	}
	params := map[string]string{}
	for _, m := range challengeRE.FindAllStringSubmatch(challenge, -1) {
		params[strings.ToLower(m[1])] = m[2]
	}
	realm, err := url.Parse(params["realm"])
	if err != nil || (realm.Scheme != "https" && !p.PlainHTTP[realm.Host]) {
		return fmt.Errorf("registry %s: token realm %q is not https", r.Registry, params["realm"])
	}
	q := realm.Query()
	if s := params["service"]; s != "" {
		q.Set("service", s)
	}
	q.Set("scope", "repository:"+r.Repo+":pull")
	realm.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		// Pulls are anonymous, so a registry that will not hand out a pull
		// token is saying the repository is not public. ghcr.io answers 403
		// both for a private one and for a name that does not exist, a typo
		// included, before any tag is looked at; "token request: 403" alone
		// sent people looking at credentials sandboxd never uses.
		return fmt.Errorf("%s/%s: the registry refused an anonymous pull (token request: %s): no such image, or it is private; check the name, or make it public (sandboxd pulls without credentials)", r.Registry, r.Repo, resp.Status)
	default:
		return fmt.Errorf("token request: %s", resp.Status)
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return err
	}
	t := tok.Token
	if t == "" {
		t = tok.AccessToken
	}
	if t == "" {
		return errors.New("token response carried no token")
	}
	p.mu.Lock()
	if p.tokens == nil {
		p.tokens = map[string]string{}
	}
	p.tokens[r.Registry+"/"+r.Repo] = t
	p.mu.Unlock()
	return nil
}
