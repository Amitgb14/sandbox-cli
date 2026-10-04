package policy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A key that parses and does nothing is refused, naming what replaced it; a
// typo is refused with it; the keys the client reads, and the config beta.15's
// installer wrote (profile and network.mode: default), pass.
func TestCheckLiveKeys(t *testing.T) {
	write := func(body string) string {
		p := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, ok := range []string{
		"",
		"profile: dev\nnetwork:\n  mode: default\n",
		"image: x:1\nenv: {A: b}\nenv_allow: [N]\nsecrets: {T: {env: X}}\nrouting: [claude, codex]\nproviders: {claude: h}\npersist_auth: false\n",
	} {
		if err := CheckLiveKeys(write(ok)); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	err := CheckLiveKeys(write("security:\n  seccomp: required\nnetwrok: {mode: none}\nmounts: []\n"))
	var dead *ErrDeadKeys
	if !errors.As(err, &dead) || strings.Join(dead.Keys, ",") != "mounts,netwrok,security" {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"the boundary is a VM", "--volume", "not a setting"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
	// mirror went with the repository: a config still setting it is told so,
	// rather than believing its work is being copied somewhere.
	err = CheckLiveKeys(write("mirror:\n  s3:\n    bucket: b\n"))
	if !errors.As(err, &dead) || strings.Join(dead.Keys, ",") != "mirror" || !strings.Contains(err.Error(), "no repository") {
		t.Errorf("a mirror block: %v", err)
	}
	if CheckLiveKeys(filepath.Join(t.TempDir(), "absent.yaml")) != nil || CheckLiveKeys("") != nil {
		t.Error("an absent file is not an error")
	}
}

// A mirror block is checked whole: a destination is required, a credential
// is a variable's name and nothing else, and a mode nobody defines is refused.
func TestMirrorIsValidated(t *testing.T) {
	ok := Config{Image: "x", Workdir: "/w", Mirror: &MirrorSpec{S3: &S3Spec{Bucket: "b", AccessKeyEnv: "MY_KEY"}}}
	if err := validateMirror(ok.Mirror); err != nil {
		t.Errorf("a valid block: %v", err)
	}
	for name, m := range map[string]*MirrorSpec{
		"no bucket":             {S3: &S3Spec{}},
		"no s3 at all":          {Upload: MirrorAll},
		"an unknown mode":       {S3: &S3Spec{Bucket: "b"}, Upload: "sometimes"},
		"a negative ceiling":    {S3: &S3Spec{Bucket: "b"}, MaxObjectMB: -1},
		"a key where a name is": {S3: &S3Spec{Bucket: "b", SecretKeyEnv: "wJalrXUtnFEMI/K7MDENG"}},
	} {
		if err := validateMirror(m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (&MirrorSpec{}).MaxObjectBytes() != DefaultMirrorMaxObjectMB<<20 || (*MirrorSpec)(nil).UploadMode() != MirrorBringBack {
		t.Error("defaults")
	}
}

// The default image is one a registry serves: a host, a path and a tag. The
// bare name it used to be was beta.15's local docker tag, and a sandboxd
// started without --default-image could boot nothing.
func TestDefaultImageIsPullable(t *testing.T) {
	host, rest, ok := strings.Cut(DefaultImage, "/")
	if !ok || !strings.Contains(host, ".") || !strings.Contains(rest, ":") {
		t.Errorf("DefaultImage %q is not a fully qualified image reference", DefaultImage)
	}
}
