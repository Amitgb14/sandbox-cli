package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// GuestAgentPath is where the guest agent lives inside every root disk.
const GuestAgentPath = "/sbin/sandbox-guestd"

// ImageConfigPath is where the image's own environment is recorded for the
// guest agent, which starts processes with it.
const ImageConfigPath = "/etc/sandbox/image.json"

// rootfsFormat changes when what Build puts in a disk changes, so a cached disk
// from an older format is not reused.
const rootfsFormat = "1"

// RootFS is a bootable, read-only root disk for an image.
type RootFS struct {
	Path   string // the ext4 file
	Key    string // what it is cached under
	Config Config
	Stats  UnpackStats
	// OwnedByHost is set when the disk was built without root: every file in it
	// belongs to the building user rather than to the owner the image declared.
	OwnedByHost bool
}

// BuildRootFS pulls ref and turns it into a root disk under dir, reusing one
// already built for the same image and the same guest agent. agent is the
// sandbox-guestd binary (built for the guest's architecture) to put in it.
//
// Run as root on a server, the disk keeps the image's ownership. Run unprivileged
// — development — every file is owned by the building user; the guest still
// boots, but setuid tools and root-owned configuration will not behave as the
// image intended, and RootFS.OwnedByHost says so.
func BuildRootFS(ctx context.Context, p *Puller, ref, agent, dir string) (*RootFS, error) {
	pulled, err := p.Pull(ctx, ref)
	if err != nil {
		return nil, err
	}
	agentSum, err := fileSHA(agent)
	if err != nil {
		return nil, fmt.Errorf("guest agent: %w", err)
	}
	keySum := sha256.Sum256([]byte(rootfsFormat + "\x00" + pulled.Digest + "\x00" + agentSum))
	key := hex.EncodeToString(keySum[:16])
	out := filepath.Join(dir, "rootfs", key, "rootfs.ext4")
	res := &RootFS{Path: out, Key: key, Config: pulled.Config, OwnedByHost: os.Geteuid() != 0}
	// Sandboxes created together ask for the same disk together — several
	// agents started at once do it every time. The second waits for the first build rather than racing
	// it; across processes, each build writes its own partial file and the
	// renames are atomic.
	unlock := lockKey(out)
	defer unlock()
	if _, err := os.Stat(out); err == nil {
		// A disk from before disks were labelled gets its label here: its
		// key proves which agent it holds.
		labelAgent(filepath.Dir(out), agentSum)
		return res, nil
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(out), ".stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	// The staging root is reached only by its creator while it is filled.
	if err := os.Chmod(stage, 0o700); err != nil {
		return nil, err
	}
	for _, l := range pulled.Layers {
		f, err := os.Open(l)
		if err != nil {
			return nil, err
		}
		err = Unpack(stage, f, 64<<30, &res.Stats)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
	}
	if err := inject(stage, agent, pulled.Config); err != nil {
		return nil, err
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return nil, err
	}
	size, err := diskSize(stage)
	if err != nil {
		return nil, err
	}
	tmp := out + ".partial" + strings.TrimPrefix(filepath.Base(stage), ".stage")
	if err := mkfs(ctx, stage, tmp, size); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if res.Stats.UnmappedOwners > 0 {
		res.OwnedByHost = true
	}
	if err := os.Rename(tmp, out); err != nil {
		return nil, err
	}
	labelAgent(filepath.Dir(out), agentSum)
	return res, nil
}

// inject adds what every sandbox needs regardless of the image: the guest
// agent, the mount points init uses, the workspace, and the image's own
// environment.
func inject(root, agent string, cfg Config) error {
	for _, d := range []string{"proc", "sys", "dev", "run", "tmp", "mnt", "workspace", "sbin", "etc/sandbox"} {
		p, err := resolveDir(root, d)
		if err != nil {
			return fmt.Errorf("preparing /%s: %w", d, err)
		}
		if d == "tmp" {
			_ = os.Chmod(p, 0o1777)
		}
	}
	sbin, _ := resolveDir(root, "sbin")
	dst := filepath.Join(sbin, filepath.Base(GuestAgentPath))
	if err := replace(dst); err != nil {
		return err
	}
	if err := copyFile(agent, dst, 0o755); err != nil {
		return err
	}
	etc, _ := resolveDir(root, "etc/sandbox")
	b, _ := json.MarshalIndent(cfg, "", "  ")
	cfgPath := filepath.Join(etc, filepath.Base(ImageConfigPath))
	if err := replace(cfgPath); err != nil {
		return err
	}
	return os.WriteFile(cfgPath, b, 0o644)
}

// diskSize is the content plus headroom for ext4's own metadata, in MiB. The
// disk is read-only to the guest — writes land on the per-sandbox scratch disk —
// so it needs no free space of its own beyond that.
func diskSize(root string) (int64, error) {
	var bytes, entries int64
	err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				bytes += fi.Size()
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	total := bytes + entries*4096
	mib := total*5/4/(1<<20) + 64
	return mib, nil
}

func mkfs(ctx context.Context, root, out string, sizeMiB int64) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := f.Truncate(sizeMiB << 20); err != nil {
		f.Close()
		return err
	}
	f.Close()
	cmd := exec.CommandContext(ctx, "mkfs.ext4", "-q", "-F", "-L", "sbxroot",
		"-E", "root_owner=0:0", "-d", root, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4 (%d MiB): %v: %s", sizeMiB, err, b)
	}
	return nil
}

// MakeScratch creates a sandbox's writable disk: a sparse ext4 file, so its size
// is a ceiling rather than an allocation.
func MakeScratch(ctx context.Context, path string, sizeMiB int) error {
	// mkfs.ext4 -d with an empty directory is a plain format.
	empty, err := os.MkdirTemp(filepath.Dir(path), ".empty-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(empty)
	return mkfs(ctx, empty, path, int64(sizeMiB))
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var (
	keyLocksMu sync.Mutex
	keyLocks   = map[string]*sync.Mutex{}
)

func lockKey(k string) func() {
	keyLocksMu.Lock()
	m, ok := keyLocks[k]
	if !ok {
		m = &sync.Mutex{}
		keyLocks[k] = m
	}
	keyLocksMu.Unlock()
	m.Lock()
	return m.Unlock
}
