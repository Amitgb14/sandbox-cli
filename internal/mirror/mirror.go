// Package mirror copies work that came back from a sandbox to object storage,
// and fetches it back, so it outlives the machine that ran it. Ported in
// spirit from beta.15's line after 0.0.1 (_old/internal/rescue/remote.go); the
// S3 client came across whole (internal/s3).
//
// The rules it keeps, each from docs/security/open-items.md:
//
//   - The configuration names credentials and never holds them (policy.S3Spec);
//     a repository cannot set any of it (policy/trust.go).
//   - A bundle is self-contained — the ref and its whole history — so it can be
//     fetched into any clone of the repository, on any machine, even one that
//     never had the commit the run started from. It is sized like a clone for
//     that reason, and refused up front past the configured ceiling.
//   - A fetched bundle is checked before anything lands, and lands only under
//     refs/sandbox/mirror/: it verifies against the repository, carries exactly
//     one ref, that ref is the commit the object's name says, it descends from
//     this repository's root, and — where this machine recorded the upload —
//     it is the commit that was recorded. A bucket can be shared, mistaken or
//     tampered with; `git bundle verify` alone would accept somebody else's
//     history served under this key.
//   - Nothing here deletes from the bucket. Retention there is the bucket's
//     lifecycle rules: deleting an off-machine copy on a timer that only runs
//     while this machine is up is a way to lose the copy meant to survive it.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/s3"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// Kinds of mirrored work.
const (
	KindBringBack  = "bring-back"
	KindCheckpoint = "checkpoint"
)

// RefPrefix is where fetched work lands.
const RefPrefix = "refs/sandbox/mirror/"

// Mirror is one bucket, for one repository.
type Mirror struct {
	spec    *policy.MirrorSpec
	client  *s3.Client
	repo    string
	repoKey string
}

// Entry is one mirrored object.
type Entry struct {
	Key  string    `json:"key"`
	SHA  string    `json:"sha"`
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
	Size int64     `json:"size,omitempty"`
}

// New resolves the credentials the spec names and the repository's key.
func New(spec *policy.MirrorSpec, repo string) (*Mirror, error) {
	if spec == nil || spec.S3 == nil {
		return nil, errors.New("no mirror is configured (mirror.s3 in ~/.config/sandbox/config.yaml)")
	}
	c, err := s3.New(s3.Config{
		Bucket: spec.S3.Bucket, Region: spec.S3.Region, Endpoint: spec.S3.Endpoint,
		Prefix: spec.S3.Prefix, PathStyle: spec.S3.PathStyle,
		AccessKeyEnv: spec.S3.AccessKeyEnv, SecretKeyEnv: spec.S3.SecretKeyEnv, SessionTokenEnv: spec.S3.SessionTokenEnv,
	})
	if err != nil {
		return nil, err
	}
	key, err := RepoKey(repo)
	if err != nil {
		return nil, err
	}
	return &Mirror{spec: spec, client: c, repo: repo, repoKey: key}, nil
}

// RepoKey names a repository in the bucket by its root commit, which every
// clone of it shares, so the same repository on another machine finds its own
// objects; a path would differ per machine. With several roots, the smallest.
func RepoKey(repo string) (string, error) {
	out, err := workspace.Git(repo, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", fmt.Errorf("the repository has no commit to name it by: %w", err)
	}
	roots := strings.Fields(out)
	if len(roots) == 0 {
		return "", errors.New("the repository has no root commit")
	}
	sort.Strings(roots)
	return roots[0][:16], nil
}

// Bucket names the destination, for messages.
func (m *Mirror) Bucket() string { return m.client.Bucket() }

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// Upload copies ref, with its whole history, to the bucket.
func (m *Mirror) Upload(ctx context.Context, ref, sandbox, kind string, now time.Time) (Entry, error) {
	sha, err := workspace.Git(m.repo, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil || !shaRE.MatchString(sha) {
		return Entry{}, fmt.Errorf("%s does not name a commit: %v", ref, err)
	}
	tmp, err := os.CreateTemp("", "sbx-mirror-*.bundle")
	if err != nil {
		return Entry{}, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if _, err := workspace.Git(m.repo, "bundle", "create", "--quiet", tmp.Name(), ref); err != nil {
		return Entry{}, err
	}
	fi, err := os.Stat(tmp.Name())
	if err != nil {
		return Entry{}, err
	}
	if max := m.spec.MaxObjectBytes(); fi.Size() > max {
		return Entry{}, fmt.Errorf("the bundle is %d MiB, over mirror.max_object_mb (%d MiB); not uploaded", fi.Size()>>20, max>>20)
	}
	e := Entry{SHA: sha, Kind: kind, At: now.UTC(), Size: fi.Size(),
		Key: path.Join(m.repoKey, sandbox, fmt.Sprintf("%s-%s-%s.bundle", kind, now.UTC().Format("20060102T150405Z"), sha))}
	if err := m.client.Put(ctx, e.Key, tmp.Name(), "application/x-git-bundle"); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// keyRE is a key this package wrote: kind, time, the full commit.
var keyRE = regexp.MustCompile(`^([0-9a-f]{16})/(sbx_[0-9a-f]+)/(bring-back|checkpoint)-(\d{8}T\d{6}Z)-([0-9a-f]{40}(?:[0-9a-f]{24})?)\.bundle$`)

// List is this repository's mirrored work, newest first, for one sandbox or
// (sandbox "") all of them. Objects whose names this package did not write are
// left out: the bucket may hold other things.
func (m *Mirror) List(ctx context.Context, sandbox string) ([]Entry, error) {
	prefix := m.repoKey + "/"
	if sandbox != "" {
		prefix += sandbox + "/"
	}
	objs, err := m.client.List(ctx, prefix) // the client adds the configured prefix
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, o := range objs {
		rel := strings.TrimPrefix(o.Key, m.client.Key(""))
		if e, ok := parseKey(rel); ok {
			e.Size = o.Size
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

func parseKey(key string) (Entry, bool) {
	g := keyRE.FindStringSubmatch(key)
	if g == nil {
		return Entry{}, false
	}
	at, err := time.Parse("20060102T150405Z", g[4])
	if err != nil {
		return Entry{}, false
	}
	return Entry{Key: key, Kind: g[3], At: at, SHA: g[5]}, true
}

// Fetch brings a mirrored object back into refs/sandbox/mirror/<sandbox>,
// checking it first (see the package comment). recorded is the commit this
// machine recorded when it uploaded the object, or "" when there is no record
// — fetching on another machine — and then the checks that remain are said
// to the caller as a weaker guarantee rather than skipped silently.
func (m *Mirror) Fetch(ctx context.Context, key, recorded string) (ref string, err error) {
	e, ok := parseKey(key)
	if !ok || !strings.HasPrefix(key, m.repoKey+"/") {
		return "", fmt.Errorf("%q is not a mirror object of this repository", key)
	}
	if recorded != "" && recorded != e.SHA {
		return "", fmt.Errorf("%s was recorded as %s here, and its name says %s; not fetching it", key, recorded, e.SHA)
	}
	tmp, err := os.CreateTemp("", "sbx-mirror-*.bundle")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := m.client.Get(ctx, key, tmp.Name()); err != nil {
		return "", err
	}
	if _, err := workspace.Git(m.repo, "bundle", "verify", "--quiet", tmp.Name()); err != nil {
		return "", fmt.Errorf("the bundle does not verify: %w", err)
	}
	heads, err := workspace.Git(m.repo, "bundle", "list-heads", tmp.Name())
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(heads), "\n")
	fields := strings.Fields(lines[0])
	if len(lines) != 1 || len(fields) != 2 || fields[0] != e.SHA {
		return "", fmt.Errorf("the bundle does not carry exactly the commit its name says (%s); not fetching it", e.SHA)
	}
	sandbox := strings.Split(key, "/")[1]
	ref = RefPrefix + sandbox
	if _, err := workspace.Git(m.repo, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--force",
		tmp.Name(), fields[1]+":"+ref); err != nil {
		return "", err
	}
	// The history has to be this repository's: a well-formed bundle of some
	// other repository, served under this key, verifies and fetches just as
	// well. It is rolled back if it is not.
	root, _ := workspace.Git(m.repo, "rev-list", "--max-parents=0", ref)
	if !strings.Contains(" "+strings.Join(strings.Fields(root), " ")+" ", " "+fullRoot(m.repo, m.repoKey)+" ") {
		_, _ = workspace.Git(m.repo, "update-ref", "-d", ref)
		return "", errors.New("the bundle's history is not this repository's; not kept")
	}
	return ref, nil
}

// fullRoot is the root commit RepoKey abbreviated.
func fullRoot(repo, key string) string {
	out, _ := workspace.Git(repo, "rev-list", "--max-parents=0", "HEAD")
	for _, r := range strings.Fields(out) {
		if strings.HasPrefix(r, key) {
			return r
		}
	}
	return key
}

// Check asks the bucket whether these credentials can reach it.
func (m *Mirror) Check(ctx context.Context) error { return m.client.Check(ctx) }
