package rescue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/s3"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// A snapshot mirrored to object storage travels as a **git bundle**, and the
// choice is worth stating because it decides what the remote copy is worth.
//
// A bundle is a packfile with a ref table, so the object in the bucket is not an
// archive that needs this tool to be useful — git alone opens it, on a machine
// that has never seen the repository:
//
//	git init recovered && cd recovered
//	git fetch ../snap.bundle 'refs/sandbox/snapshots/*:refs/heads/snap/*'
//	git checkout snap/<id>
//
// A `git clone` of it does *not* work, and the reason is worth knowing rather
// than rediscovering: a snapshot ref lives under refs/sandbox/, so the bundle
// carries no branch and no HEAD for clone to check out. Naming one would mean
// writing a refs/heads ref into the user's repository to bundle it from, which
// is the one thing this package promises never to do. Fetch-then-checkout is the
// price, and it is two commands.
//
// A tarball of the working tree would have been smaller and simpler and would
// have lost the commit identity — which is exactly what makes a restore
// verifiable, and what lets `git bundle verify` reject a truncated download
// before anything is unpacked.
//
// The cost is named rather than solved: the bundle is **self-contained**, so it
// carries history from the root and is sized like a clone rather than like a
// diff. That is why the default upload mode is manual — see config.UploadManual
// — and why MaxObjectBytes refuses a bundle rather than discovering the limit at
// the end of a long transfer.
const (
	bundleContentType   = "application/x-git-bundle"
	manifestContentType = "application/json"
)

// remoteTimeout bounds one mirror operation. Generous because a bundle is
// measured in megabytes on a connection nobody chose, and the alternative to
// waiting is a checkpoint that reports failure while the upload is still in
// flight.
const remoteTimeout = 20 * time.Minute

// RemoteRef is what the manifest records about a snapshot's copy in object
// storage.
//
// Recorded rather than discovered, because the alternative is a HEAD request per
// row every time anybody opens a listing — a listing of five hundred snapshots
// would make five hundred round trips to answer a question that does not change.
// The cost of recording it is that the field describes the upload rather than
// the bucket: an object deleted out from under us still reads as present here,
// which Verify is for.
type RemoteRef struct {
	Bucket     string    `json:"bucket"`
	Key        string    `json:"key"`
	UploadedAt time.Time `json:"uploaded_at"`
	Bytes      int64     `json:"bytes,omitempty"`

	// Error is why the last attempt failed, and is cleared by a success. It is
	// kept in the manifest rather than logged and forgotten so that a snapshot
	// whose upload failed is *visibly* local-only, instead of looking mirrored
	// to anybody who did not happen to be watching the terminal at the time.
	Error string `json:"error,omitempty"`
}

// Uploaded reports a copy that is believed to be in the bucket.
func (r *RemoteRef) Uploaded() bool { return r != nil && r.Key != "" && r.Error == "" }

// ErrNoRemote is returned when no bucket is configured, or when the
// configuration says not to upload this kind of snapshot.
var ErrNoRemote = errors.New("no snapshot bucket configured")

// remoteKeys renders the two object keys for a session.
//
// Two objects rather than one: the bundle, and the manifest beside it. The
// manifest is what makes the bucket **self-describing** — the branch, the agent,
// the label and the times, next to the bytes they describe — so a machine that
// has lost ~/.config/sandbox/rescue entirely can still be told what is in there
// and which of it is worth pulling back. It costs a few hundred bytes per
// snapshot, which is the cheapest part of this feature by four orders of
// magnitude.
//
// Keyed by repository **id** rather than by path, for the reason labels are: two
// clones of a same-named repository would otherwise share a namespace, and the
// second one to upload would overwrite the first.
func remoteKeys(repoID, sessionID string) (bundle, manifest string) {
	base := s3.JoinKey("snapshots", repoID, sessionID)
	return base + ".bundle", base + ".json"
}

// remoteRepoID is the bucket namespace for a repository, matching the one that
// names its worktrees and stamps its containers. A repository that cannot be
// identified falls back to its directory name, which is the same bargain
// sessionsDir makes and costs no more than a stale prefix.
func remoteRepoID(repoRoot string) string {
	if id, err := worktree.RepoID(repoRoot); err == nil {
		return id
	}
	return filepath.Base(repoRoot)
}

// Mirror uploads one snapshot's bundle and manifest, records the result on the
// session, and saves it.
//
// It returns an error *and* records it, which is not redundancy: the caller
// decides whether a failed mirror should fail the operation — a manual
// checkpoint says yes, the crash-net loop says no — while the manifest is what
// tells the person looking at a listing tomorrow that this one never left the
// machine.
func Mirror(ctx context.Context, sess *Session, spec *config.S3Spec) error {
	if spec == nil || spec.Bucket == "" {
		return ErrNoRemote
	}
	if sess.LastSnapshot == "" {
		// Nothing was ever captured, so there is nothing to bundle. Reached by a
		// caller mirroring a session it did not create.
		return ErrNothingToSnapshot
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}

	dir, err := os.MkdirTemp("", "sandbox-bundle-")
	if err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}
	defer os.RemoveAll(dir)
	bundlePath := filepath.Join(dir, sess.ID+".bundle")

	if err := writeBundle(ctx, sess, bundlePath); err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}
	st, err := os.Stat(bundlePath)
	if err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}
	if max := spec.MaxObjectBytes(); st.Size() > max {
		// Refused here rather than at the far end of an upload that was always
		// going to be rejected. The message names both numbers because the
		// remedy — raise max_object_mb, or stop mirroring this repository — needs
		// to know how far apart they are.
		return recordFailure(sess, spec.Bucket, fmt.Errorf(
			"bundle is %s, over the %s limit (snapshot.s3.max_object_mb); this client does not do multipart uploads",
			HumanBytes(st.Size()), HumanBytes(max)))
	}

	bundleKey, manifestKey := remoteKeys(remoteRepoID(sess.Repo), sess.ID)
	if err := client.Put(ctx, bundleKey, bundlePath, bundleContentType); err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}

	// The manifest is written second and on purpose. Its presence is what says
	// "there is a complete snapshot here", so an upload interrupted between the
	// two leaves an orphan bundle rather than a manifest advertising bytes that
	// never arrived.
	manifestPath := filepath.Join(dir, sess.ID+".json")
	if err := writeSessionJSON(*sess, manifestPath); err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}
	if err := client.Put(ctx, manifestKey, manifestPath, manifestContentType); err != nil {
		return recordFailure(sess, spec.Bucket, err)
	}

	sess.Remote = &RemoteRef{
		Bucket:     spec.Bucket,
		Key:        bundleKey,
		UploadedAt: time.Now(),
		Bytes:      st.Size(),
	}
	return sess.Save()
}

// recordFailure stamps why the mirror failed and returns the error unchanged.
//
// A save that itself fails is swallowed: the caller is already being handed the
// real problem, and replacing "the bucket refused this" with "could not write a
// manifest" would hide the thing they can act on.
func recordFailure(sess *Session, bucket string, err error) error {
	sess.Remote = &RemoteRef{Bucket: bucket, Error: err.Error(), UploadedAt: time.Now()}
	_ = sess.Save()
	return err
}

// writeBundle asks git for a self-contained bundle of the snapshot ref.
//
// Through run(), so it inherits githard's hardening like every other git call
// this package makes on its own behalf: bundling walks the object graph of a
// repository the agent has been writing to, and `gc.auto=0` matters here more
// than anywhere — packing is exactly when git would otherwise decide this is a
// good moment to repack somebody's repository.
//
// The ref is preferred over the recorded sha so the bundle carries a ref table
// somebody can clone; a session whose ref has been pruned but whose objects
// survive falls back to the sha under the ref name it used to have.
func writeBundle(ctx context.Context, sess *Session, dest string) error {
	ref := sess.Ref
	if ref == "" {
		ref = RefPrefix + sess.ID
	}
	if sha, err := run(ctx, sess.Repo, nil, "rev-parse", "--verify", "--quiet", ref); err != nil || sha == "" {
		// `git bundle create <file> <sha>` produces a bundle with no ref in it,
		// which git will not clone from — so the sha is given a name.
		if _, err := run(ctx, sess.Repo, nil, "bundle", "create", dest,
			sess.LastSnapshot+":"+ref); err != nil {
			return fmt.Errorf("bundling snapshot %s: %w", sess.ID, err)
		}
		return nil
	}
	if _, err := run(ctx, sess.Repo, nil, "bundle", "create", dest, ref); err != nil {
		return fmt.Errorf("bundling snapshot %s: %w", sess.ID, err)
	}
	return nil
}

// Fetch pulls a snapshot's bundle back from the bucket and restores its ref into
// the repository, so every existing restore mode works on it unchanged.
//
// This is the whole reason mirroring is a *backup* rather than an offload: the
// local ref is what restore reads, so bringing one back means putting the
// objects where they always were rather than teaching three restore modes about
// the network. Nothing else in this package needs to know a bucket exists.
//
// It only ever writes under refs/sandbox/, the rule this package keeps
// everywhere: a bundle is a packfile from off this machine, so it is unpacked
// into the namespace sandbox-cli owns and never allowed to name a branch.
func Fetch(ctx context.Context, sess *Session, spec *config.S3Spec) error {
	if spec == nil || spec.Bucket == "" {
		return ErrNoRemote
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return err
	}
	ref := sess.Ref
	if ref == "" {
		ref = RefPrefix + sess.ID
	}
	// Checked before the download, not after it. A manifest is a file on disk and
	// this one names the ref to write; it has never been anything but ours, and
	// it is not going to start being a branch name because somebody edited a json
	// file. Refusing first also means a bad manifest costs nothing to reject
	// rather than a bundle-sized transfer.
	if !strings.HasPrefix(ref, RefPrefix) {
		return fmt.Errorf("refusing to fetch %s into %q: snapshots live under %s", sess.ID, ref, RefPrefix)
	}

	key := ""
	if sess.Remote != nil {
		key = sess.Remote.Key
	}
	if key == "" {
		key, _ = remoteKeys(remoteRepoID(sess.Repo), sess.ID)
	}

	dir, err := os.MkdirTemp("", "sandbox-bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	local := filepath.Join(dir, sess.ID+".bundle")
	if err := client.Get(ctx, key, local); err != nil {
		if errors.Is(err, s3.ErrNotFound) {
			return fmt.Errorf("snapshot %s is not in %s (key %s)", sess.ID, spec.Bucket, key)
		}
		return err
	}

	// Verified before it is fetched from. `git bundle verify` is what turns a
	// truncated download or a bucket serving somebody's error page into a
	// refusal here, rather than a half-unpacked object store discovered later.
	if _, err := run(ctx, sess.Repo, nil, "bundle", "verify", local); err != nil {
		return fmt.Errorf("the bundle for %s did not verify: %w", sess.ID, err)
	}

	if _, err := run(ctx, sess.Repo, nil, "fetch", "--no-write-fetch-head", local, ref+":"+ref); err != nil {
		return fmt.Errorf("unpacking the bundle for %s: %w", sess.ID, err)
	}

	// What came back must be what went up. `git bundle verify` establishes that
	// the bundle is internally consistent, which is a different claim: it would
	// pass just as happily on a perfectly-formed bundle of somebody else's
	// commit, served under this key by a bucket that was tampered with, shared
	// by mistake, or simply overwritten by another machine using the same prefix.
	//
	// The manifest on this machine recorded the sha when the snapshot was taken,
	// so there is a local answer to compare against — and a restore that quietly
	// returns the wrong tree is the exact failure mode this feature was built in
	// the shadow of. The ref is rolled back rather than left pointing at content
	// nobody asked for.
	if want := sess.LastSnapshot; want != "" {
		got, err := run(ctx, sess.Repo, nil, "rev-parse", "--verify", "--quiet", ref)
		if err != nil || got != want {
			_, _ = run(ctx, sess.Repo, nil, "update-ref", "-d", ref)
			return fmt.Errorf(
				"the bundle for %s holds %s, but this machine recorded the snapshot as %s; refusing it",
				sess.ID, shortSHA(got), shortSHA(want))
		}
	}
	return nil
}

// shortSHA abbreviates for a message, and says so when there is nothing to
// abbreviate — "" in the middle of a sentence about mismatched shas reads as a
// formatting bug rather than as the absence it is.
func shortSHA(s string) string {
	switch {
	case s == "":
		return "nothing"
	case len(s) > 12:
		return s[:12]
	default:
		return s
	}
}

// Verify asks the bucket whether a snapshot's object is actually there, and
// reports its size.
//
// Separate from the manifest for the reason RemoteRef documents: the manifest
// records what an upload did, and an object deleted out from under it — a
// lifecycle rule, somebody tidying a bucket — leaves a snapshot that reads as
// mirrored and is not. This is the one call that asks rather than remembers.
func Verify(ctx context.Context, sess Session, spec *config.S3Spec) (int64, error) {
	if spec == nil || spec.Bucket == "" {
		return 0, ErrNoRemote
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return 0, err
	}
	key := ""
	if sess.Remote != nil {
		key = sess.Remote.Key
	}
	if key == "" {
		key, _ = remoteKeys(remoteRepoID(sess.Repo), sess.ID)
	}
	return client.Stat(ctx, key)
}

// remoteListLimit bounds how many manifests a bucket listing reads back. Each
// one is a separate GET, so an unbounded listing of a long-lived bucket is a
// thousand round trips before the first line of output. The cap is stated in
// what RemoteSessions returns rather than applied silently — files.go's rule: a
// listing that stops without saying so reads as "this is everything".
const remoteListLimit = 200

// RemoteNamespace is the prefix a repository's snapshots live under in the
// bucket, which is its RepoID — the same id that names its worktrees and stamps
// its containers.
//
// Exported because that id is a hash of the repository's **absolute path**, so a
// repository re-cloned somewhere else has a different one, and the snapshots
// uploaded from its old location are under a prefix nothing here would think to
// look in. A caller that lets the user name the namespace can reach them; one
// that derives it can only ever find snapshots from a repository that has not
// moved.
func RemoteNamespace(repoRoot string) string { return remoteRepoID(repoRoot) }

// RemoteSessions reads what the bucket holds for one repository, from the
// manifests written beside the bundles.
//
// This is the half of "self-describing" that remoteKeys pays a few hundred bytes
// per snapshot for: a machine that has lost ~/.config/sandbox/rescue entirely —
// or never had it, because the repository was cloned onto it this morning — can
// still be told what is in there and which of it is worth pulling back.
//
// repoID selects the namespace and defaults to this repository's own. repoRoot
// is where the snapshots would be brought back to, and the returned sessions are
// rewritten to point at it: a manifest describes the machine that wrote it, and
// the repository it is being restored into is this one. That rewrite is what
// makes them usable by Fetch and Save without the caller reaching into fields it
// should not have to know about.
//
// What comes back is *not* a local record, and the difference matters at exactly
// one point: Fetch compares the bundle's commit against the sha the manifest
// recorded, and for these two the manifest and the bundle came from the same
// bucket. That comparison then proves the pair are consistent with each other,
// which is a weaker claim than the one it makes for a session this machine
// captured itself. Callers should say so rather than let it read as verified.
//
// Returns the sessions, newest first, and the total number of manifests found —
// which is larger than len(sessions) when the listing was capped, or when a
// manifest could not be read.
func RemoteSessions(ctx context.Context, spec *config.S3Spec, repoRoot, repoID string) ([]Session, int, error) {
	if spec == nil || spec.Bucket == "" {
		return nil, 0, ErrNoRemote
	}
	if repoID == "" {
		repoID = remoteRepoID(repoRoot)
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return nil, 0, err
	}
	objects, err := client.List(ctx, s3.JoinKey("snapshots", repoID)+"/")
	if err != nil {
		return nil, 0, err
	}

	// Sizes come from the listing that is already in hand. The alternative is a
	// HEAD per row, which is the cost RemoteRef exists to avoid.
	bundleSize := map[string]int64{}
	var manifests []s3.Object
	for _, o := range objects {
		switch {
		case strings.HasSuffix(o.Key, ".json"):
			manifests = append(manifests, o)
		case strings.HasSuffix(o.Key, ".bundle"):
			bundleSize[sessionIDOfKey(o.Key)] = o.Size
		}
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Modified.After(manifests[j].Modified) })
	total := len(manifests)
	if len(manifests) > remoteListLimit {
		manifests = manifests[:remoteListLimit]
	}

	dir, err := os.MkdirTemp("", "sandbox-manifest-")
	if err != nil {
		return nil, total, err
	}
	defer os.RemoveAll(dir)

	out := make([]Session, 0, len(manifests))
	for i, m := range manifests {
		id := sessionIDOfKey(m.Key)
		if id == "" {
			continue
		}
		// Keys are rebuilt from the id rather than taken from the listing, which
		// returns them with the client's configured prefix already applied — and
		// Get applies it again. Reconstructing is how that double prefix stays
		// impossible rather than merely untested.
		bundleKey, manifestKey := remoteKeys(repoID, id)
		local := filepath.Join(dir, fmt.Sprint(i)+".json")
		if err := client.Get(ctx, manifestKey, local); err != nil {
			// One unreadable manifest must not hide every other recoverable
			// snapshot, the same bargain sessionsIn makes with a corrupt file on
			// disk. It is not silent: total still counts it.
			continue
		}
		sess, err := loadSession(local)
		if err != nil {
			continue
		}
		sess.Repo = repoRoot
		// The manifest is a download in a temp directory that is about to be
		// removed, and Save() would write the session back into it. Clearing the
		// path is what makes Save fall through to this machine's rescue
		// directory, which is where a fetched snapshot belongs.
		sess.path = ""
		// Recorded from the listing, not from the manifest: the copy uploaded to
		// the bucket is written before Mirror stamps the RemoteRef, so the one in
		// there always says nil. This is also what lets Fetch address a namespace
		// the local repository would not have computed.
		sess.Remote = &RemoteRef{
			Bucket:     spec.Bucket,
			Key:        bundleKey,
			UploadedAt: m.Modified,
			Bytes:      bundleSize[id],
		}
		out = append(out, sess)
	}
	sortSessions(out)
	return out, total, nil
}

// RemoteRepoIDs reports the repository namespaces the bucket holds snapshots
// for, newest object first.
//
// It exists for one question, and it is a question a hash of an absolute path
// guarantees somebody will ask: "I cloned this repository onto a new machine and
// my snapshots are not listed — where are they?" They are under the id the old
// path produced. Nothing can derive that here, so the answer is to show what is
// in the bucket and let the user name it.
func RemoteRepoIDs(ctx context.Context, spec *config.S3Spec) ([]string, error) {
	if spec == nil || spec.Bucket == "" {
		return nil, ErrNoRemote
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return nil, err
	}
	objects, err := client.List(ctx, "snapshots/")
	if err != nil {
		return nil, err
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].Modified.After(objects[j].Modified) })
	seen := map[string]bool{}
	var out []string
	for _, o := range objects {
		id := repoIDOfKey(o.Key)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// sessionIDOfKey reads the session id back out of an object key.
func sessionIDOfKey(key string) string {
	base := path.Base(key)
	return strings.TrimSuffix(strings.TrimSuffix(base, ".json"), ".bundle")
}

// repoIDOfKey reads the repository namespace out of an object key.
//
// It looks for the "snapshots/" segment rather than splitting from the left,
// because a configured Prefix sits in front of it and a listing returns keys
// with that prefix applied. Finding the marker is what keeps this correct for a
// bucket shared with anything else.
func repoIDOfKey(key string) string {
	const marker = "snapshots/"
	i := strings.Index(key, marker)
	if i < 0 {
		return ""
	}
	rest := key[i+len(marker):]
	j := strings.Index(rest, "/")
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// CheckRemote verifies that the configured bucket answers and that the named
// credentials resolve. It is what a "Test connection" button asks.
func CheckRemote(ctx context.Context, spec *config.S3Spec) error {
	if spec == nil || spec.Bucket == "" {
		return ErrNoRemote
	}
	client, err := s3.New(clientConfig(spec))
	if err != nil {
		return err
	}
	return client.Check(ctx)
}

// clientConfig translates the config block into the client's own, which are
// deliberately separate types: internal/s3 knows nothing about sandbox-cli's
// configuration, and config knows nothing about signing.
func clientConfig(spec *config.S3Spec) s3.Config {
	return s3.Config{
		Bucket:          spec.Bucket,
		Region:          spec.Region,
		Endpoint:        spec.Endpoint,
		Prefix:          spec.Prefix,
		PathStyle:       spec.PathStyle,
		AccessKeyEnv:    spec.AccessKeyEnv,
		SecretKeyEnv:    spec.SecretKeyEnv,
		SessionTokenEnv: spec.SessionTokenEnv,
	}
}

// writeSessionJSON writes a manifest to an arbitrary path, for the copy that
// goes in the bucket. Session.Save writes to the manifest's own location and is
// not reusable for this.
func writeSessionJSON(sess Session, dest string) error {
	data, err := marshalSession(sess)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o600)
}

// HumanBytes renders a byte count for a person: the size of a bundle, in the
// refusal that names the limit it went over and in the listing that says what is
// in the bucket. Exported for the second of those — `sandbox-cli recover fetch`
// reports the same sizes, and two spellings of one number in two places that
// describe the same object is how a listing starts disagreeing with an error.
func HumanBytes(n int64) string {
	const unit = 1 << 10
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}
