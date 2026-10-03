package mirror

import (
	"context"
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/mirror/mirrortest"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@x", "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func repoWithWork(t *testing.T) (repo, work string) {
	repo = t.TempDir()
	git(t, repo, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644)
	git(t, repo, "add", "a.txt")
	git(t, repo, "commit", "-qm", "base")
	git(t, repo, "checkout", "-qb", "w")
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("work\n"), 0o644)
	git(t, repo, "add", "b.txt")
	git(t, repo, "commit", "-qm", "work")
	work = git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "checkout", "-q", "main")
	git(t, repo, "update-ref", "refs/sandbox/sbx_0123456789abcdef", work)
	git(t, repo, "branch", "-D", "w")
	return repo, work
}

func newMirror(t *testing.T, repo string, spec *policy.MirrorSpec) (*Mirror, *mirrortest.Bucket) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	noBackgroundGit(t)
	f, srv := mirrortest.NewServer(t)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	if spec == nil {
		spec = &policy.MirrorSpec{}
	}
	spec.S3 = &policy.S3Spec{Bucket: "b", Endpoint: srv.URL, PathStyle: true, Prefix: "work"}
	m, err := New(spec, repo)
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

// The round trip: a ref goes up self-contained and comes back into another
// clone of the repository — another machine — that never had the commit,
// landing only under refs/sandbox/mirror/.
func TestUploadAndFetchIntoAnotherClone(t *testing.T) {
	repo, work := repoWithWork(t)
	m, f := newMirror(t, repo, nil)
	e, err := m.Upload(context.Background(), "refs/sandbox/sbx_0123456789abcdef", "sbx_0123456789abcdef", KindBringBack, time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if e.SHA != work || !strings.HasPrefix(e.Key, m.repoKey+"/sbx_0123456789abcdef/bring-back-20261003T090000Z-") {
		t.Fatalf("entry %+v", e)
	}
	if _, ok := f.Objects["work/"+e.Key]; !ok {
		t.Fatalf("not under the prefix: %v", f.Objects)
	}
	f.Objects["work/"+m.repoKey+"/notes.txt"] = []byte("somebody else's")

	other := t.TempDir()
	git(t, other, "clone", "-q", "--no-local", repo, ".")
	git(t, other, "update-ref", "-d", "refs/remotes/origin/HEAD")
	m2, _ := New(m.spec, other)
	list, err := m2.List(context.Background(), "")
	if err != nil || len(list) != 1 || list[0].SHA != work {
		t.Fatalf("listed %+v %v (objects this package did not write are left out)", list, err)
	}
	before := git(t, other, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads")
	ref, err := m2.Fetch(context.Background(), e.Key, "")
	if err != nil || ref != RefPrefix+"sbx_0123456789abcdef" || git(t, other, "rev-parse", ref) != work {
		t.Fatalf("fetch: %q %v", ref, err)
	}
	if git(t, other, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads") != before {
		t.Error("a branch moved")
	}
}

// What comes back is checked before it lands: against this machine's record
// of the upload, against its own name, and against this repository's root.
func TestFetchRefusesWhatIsNotWhatWasMirrored(t *testing.T) {
	repo, work := repoWithWork(t)
	m, f := newMirror(t, repo, nil)
	ctx := context.Background()
	e, err := m.Upload(ctx, "refs/sandbox/sbx_0123456789abcdef", "sbx_0123456789abcdef", KindCheckpoint, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Fetch(ctx, e.Key, strings.Repeat("0", 40)); err == nil {
		t.Error("an object whose name disagrees with this machine's record was fetched")
	}

	// The same key, now carrying the base commit instead of the work.
	git(t, repo, "update-ref", "refs/sandbox/sbx_0123456789abcdef", "main")
	tampered, _ := m.Upload(ctx, "refs/sandbox/sbx_0123456789abcdef", "sbx_0123456789abcdef", KindCheckpoint, time.Now().Add(time.Hour))
	f.Objects["work/"+e.Key] = f.Objects["work/"+tampered.Key]
	if _, err := m.Fetch(ctx, e.Key, work); err == nil || !strings.Contains(err.Error(), "its name says") {
		t.Errorf("a bundle that is not the commit its name says: %v", err)
	}

	// Another repository's history, named consistently, under this key.
	alien := t.TempDir()
	git(t, alien, "init", "-q", "-b", "main")
	git(t, alien, "commit", "-q", "--allow-empty", "-m", "alien")
	asha := git(t, alien, "rev-parse", "HEAD")
	git(t, alien, "update-ref", "refs/sandbox/sbx_0123456789abcdef", asha)
	am, _ := New(m.spec, alien)
	ae, err := am.Upload(ctx, "refs/sandbox/sbx_0123456789abcdef", "sbx_0123456789abcdef", KindBringBack, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	forged := m.repoKey + strings.TrimPrefix(ae.Key, am.repoKey)
	f.Objects["work/"+forged] = f.Objects["work/"+ae.Key]
	if _, err := m.Fetch(ctx, forged, ""); err == nil || !strings.Contains(err.Error(), "not this repository's") {
		t.Errorf("another repository's history: %v", err)
	}
	if out, _ := exec.Command("git", "-C", repo, "rev-parse", "-q", "--verify", RefPrefix+"sbx_0123456789abcdef").Output(); len(out) != 0 {
		t.Error("the refused history was left in the repository")
	}
}

// A bundle over the ceiling is refused before it is sent.
func TestUploadRefusesABundleOverTheCeiling(t *testing.T) {
	repo, _ := repoWithWork(t)
	big := make([]byte, 3<<20) // random, so git cannot compress it under the ceiling
	rand.Read(big)
	os.WriteFile(filepath.Join(repo, "big.bin"), big, 0o644)
	git(t, repo, "add", "big.bin")
	git(t, repo, "commit", "-qm", "big")
	git(t, repo, "update-ref", "refs/sandbox/sbx_0123456789abcdef", "HEAD")
	m, f := newMirror(t, repo, &policy.MirrorSpec{MaxObjectMB: 1})
	if _, err := m.Upload(context.Background(), "refs/sandbox/sbx_0123456789abcdef", "sbx_0123456789abcdef", KindBringBack, time.Now()); err == nil || !strings.Contains(err.Error(), "max_object_mb") {
		t.Errorf("over the ceiling: %v", err)
	}
	if len(f.Objects) != 0 {
		t.Error("something was uploaded")
	}
}

// noBackgroundGit stops a fetch from starting git's automatic gc or
// maintenance in the background, which goes on writing into the repository
// after the test has finished and races TempDir's cleanup ("directory not
// empty"). Set for every git the test starts, sandbox-cli's own included.
func noBackgroundGit(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_COUNT", "3")
	t.Setenv("GIT_CONFIG_KEY_0", "gc.auto")
	t.Setenv("GIT_CONFIG_VALUE_0", "0")
	t.Setenv("GIT_CONFIG_KEY_1", "maintenance.auto")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
	t.Setenv("GIT_CONFIG_KEY_2", "gc.autoDetach")
	t.Setenv("GIT_CONFIG_VALUE_2", "false")
}
