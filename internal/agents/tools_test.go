package agents

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every agent the image does not carry can be installed into a tools volume,
// and the ones it does carry are never given one.
func TestToolsCoverEveryBootstrappedAgent(t *testing.T) {
	name := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`) // spec.ValidName
	for _, n := range InteractiveNames() {
		d, _ := LookupInteractive(n)
		tl, ok := d.Tools()
		inImage := len(d.Command) == 1
		if ok == inImage {
			t.Errorf("%s: tools %v, but the image carries it: %v", n, ok, inImage)
			continue
		}
		if !ok {
			continue
		}
		if v := tl.Volume(n); !name.MatchString(v) {
			t.Errorf("%s: volume name %q is not a valid volume name", n, v)
		}
		if !strings.HasSuffix(tl.InstallScript(), `touch "$HOME/.sandbox-ready"`) {
			t.Errorf("%s: the ready marker is not the install's last step", n)
		}
	}
}

// A new pin is a new volume: the name follows the install script, which carries
// the version.
func TestToolsVolumeFollowsTheInstall(t *testing.T) {
	a := Tools{Bin: "x", Install: "npm install -g x@1.0.0"}
	b := Tools{Bin: "x", Install: "npm install -g x@1.0.1"}
	if a.Volume("x") == b.Volume("x") || a.Volume("x") != a.Volume("x") {
		t.Errorf("%s %s", a.Volume("x"), b.Volume("x"))
	}
}

// The bootstrap runs the agent from the tools volume only once its ready
// marker is there; a volume whose install was cut short is passed over and the
// run installs the agent itself.
func TestBootstrapUsesAReadyToolsVolume(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tools, home := t.TempDir(), t.TempDir()
	bin := filepath.Join(tools, ".local", "bin")
	os.MkdirAll(bin, 0o755)
	os.WriteFile(filepath.Join(bin, "tl"), []byte("#!/bin/sh\necho from-tools \"$@\"\n"), 0o755)
	argv := Bootstrap("tl", `mkdir -p "$HOME/.local/bin" && printf '#!/bin/sh\necho from-install\n' > "$HOME/.local/bin/tl" && chmod +x "$HOME/.local/bin/tl"`)
	delete(installs, "tl")
	script := strings.ReplaceAll(argv[2], ToolsDir, tools)
	path := "/usr/bin:/bin"
	run := func() string {
		cmd := exec.Command("sh", "-c", script, "tl", "a1")
		cmd.Env = []string{"HOME=" + home, "PATH=" + path}
		out, _ := cmd.CombinedOutput()
		return string(out)
	}
	if got := run(); !strings.Contains(got, "from-install") {
		t.Errorf("without the marker: %q", got)
	}
	os.RemoveAll(filepath.Join(home, ".local"))
	os.WriteFile(filepath.Join(tools, ".sandbox-ready"), nil, 0o644)
	if got := run(); strings.TrimSpace(got) != "from-tools a1" {
		t.Errorf("with the marker: %q", got)
	}

	// The image's copy is the one its builder chose, and wins.
	image := t.TempDir()
	os.WriteFile(filepath.Join(image, "tl"), []byte("#!/bin/sh\necho from-image\n"), 0o755)
	path = image + ":/usr/bin:/bin"
	if got := run(); strings.TrimSpace(got) != "from-image" {
		t.Errorf("with the agent in the image: %q", got)
	}
}
