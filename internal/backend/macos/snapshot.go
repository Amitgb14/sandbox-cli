package macos

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// A disk snapshot on macOS is an image. The runtime cannot capture a running
// VM's memory, but it can export a container's filesystem; that export,
// wrapped as a one-layer OCI image carrying the original image's settings and
// loaded into the runtime's own store, is what a fork runs. So a fork boots
// afresh from the files as they were: nothing that was running comes back
// (api.SnapshotDisk).
//
// Images are named for their owner, as sandboxes are labelled with it, so a
// restart removes only this sandboxd's: sandboxd keeps no snapshot records
// across a restart, and an image nobody can name again is 2 GB of nothing.
const snapshotRepo = "sbx-snapshot"

func snapshotRef(owner, snapshotID string) string {
	return snapshotRepo + "/" + owner + "/" + snapshotID + ":latest"
}

// imageConfig is what a snapshot keeps of the image it was taken from: what a
// process there starts with.
type imageConfig struct {
	Architecture string
	OS           string
	Env          []string
	WorkingDir   string
}

// configOf reads an image's settings from the runtime.
func (b *Backend) configOf(ctx context.Context, image string) (imageConfig, error) {
	out, err := b.cli(ctx, "image", "inspect", image)
	if err != nil {
		return imageConfig{}, err
	}
	var list []struct {
		Variants []struct {
			Config struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
				Config       struct {
					Env        []string `json:"Env"`
					WorkingDir string   `json:"WorkingDir"`
				} `json:"config"`
			} `json:"config"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"variants"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return imageConfig{}, fmt.Errorf("container image inspect: %w", err)
	}
	for _, img := range list {
		for _, v := range img.Variants {
			// The guest is linux/arm64; a multi-platform image lists the others too.
			if v.Platform.OS == "linux" && v.Platform.Architecture == "arm64" {
				c := v.Config
				return imageConfig{Architecture: c.Architecture, OS: c.OS, Env: c.Config.Env, WorkingDir: c.Config.WorkingDir}, nil
			}
		}
	}
	return imageConfig{}, fmt.Errorf("image %s has no linux/arm64 variant", image)
}

// headerRoom is reserved at the start of the archive for the layer's tar
// header, written once the layer's digest and size are known. A PAX header
// is forced, so the room is the same whatever the size: a PAX header block,
// its one block of records, and the USTAR header after them.
const headerRoom = 3 * 512

// writeImageArchive writes an OCI image archive to f: the layer read from
// layer, then the config, manifest, index and layout, naming the image ref.
// One pass over the layer and one file: the layer's tar entry is named for its
// digest, known only once it has been read, so its header is written into
// room left for it at the start. `container image load` reads a file, not a
// pipe, and an export is the size of the sandbox's disk, so a second copy was
// not an option.
func writeImageArchive(f *os.File, layer io.Reader, ic imageConfig, ref string, labels map[string]string) (int64, error) {
	if _, err := f.Seek(headerRoom, io.SeekStart); err != nil {
		return 0, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), layer)
	if err != nil {
		return 0, fmt.Errorf("reading the layer: %w", err)
	}
	if pad := (512 - n%512) % 512; pad > 0 {
		if _, err := f.Write(make([]byte, pad)); err != nil {
			return 0, err
		}
	}
	layerDigest := hex.EncodeToString(h.Sum(nil))
	head, err := layerHeader(layerDigest, n)
	if err != nil {
		return 0, err
	}
	if _, err := f.WriteAt(head, 0); err != nil {
		return 0, err
	}

	tw := tar.NewWriter(f)
	blob := func(v any) (string, int, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", 0, err
		}
		sum := sha256.Sum256(b)
		d := hex.EncodeToString(sum[:])
		if err := writeEntry(tw, "blobs/sha256/"+d, b); err != nil {
			return "", 0, err
		}
		return d, len(b), nil
	}
	cfgDigest, cfgSize, err := blob(map[string]any{
		"architecture": ic.Architecture,
		"os":           ic.OS,
		"config":       map[string]any{"Env": ic.Env, "WorkingDir": ic.WorkingDir, "Labels": labels},
		"rootfs":       map[string]any{"type": "layers", "diff_ids": []string{"sha256:" + layerDigest}},
	})
	if err != nil {
		return 0, err
	}
	manDigest, manSize, err := blob(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": "sha256:" + cfgDigest, "size": cfgSize},
		"layers":        []any{map[string]any{"mediaType": "application/vnd.oci.image.layer.v1.tar", "digest": "sha256:" + layerDigest, "size": n}},
	})
	if err != nil {
		return 0, err
	}
	index, _ := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"manifests": []any{map[string]any{
			"mediaType":   "application/vnd.oci.image.manifest.v1+json",
			"digest":      "sha256:" + manDigest,
			"size":        manSize,
			"platform":    map[string]string{"architecture": ic.Architecture, "os": ic.OS},
			"annotations": map[string]string{"org.opencontainers.image.ref.name": ref, "io.containerd.image.name": ref},
		}},
	})
	if err := writeEntry(tw, "index.json", index); err != nil {
		return 0, err
	}
	if err := writeEntry(tw, "oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)); err != nil {
		return 0, err
	}
	return n, tw.Close()
}

// layerHeader is the layer's tar header, exactly headerRoom long. PAX is
// forced so the length does not depend on the size: past 8 GiB, which a
// sandbox's disk may be, USTAR cannot say the size at all, and a PAX header
// that appeared only then would not fit the room left for it.
func layerHeader(digest string, size int64) ([]byte, error) {
	var head bytes.Buffer
	hw := tar.NewWriter(&head)
	if err := hw.WriteHeader(&tar.Header{
		Name: "blobs/sha256/" + digest, Mode: 0o644, Size: size, Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		PAXRecords: map[string]string{"comment": "sandbox-cli disk snapshot layer"},
	}); err != nil {
		return nil, err
	}
	if head.Len() != headerRoom {
		return nil, fmt.Errorf("the layer's header is %d bytes, not the %d left for it", head.Len(), headerRoom)
	}
	return head.Bytes(), nil
}

func writeEntry(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

// Snapshot captures the sandbox's files as an image a fork can run. The
// sandbox keeps running; what it writes during the export may or may not be
// in it, as with any copy of a live filesystem.
func (b *Backend) Snapshot(ctx context.Context, id, snapshotID string) (backend.SnapshotInfo, error) {
	b.mu.Lock()
	sp, ok := b.specs[id]
	b.mu.Unlock()
	if !ok {
		return backend.SnapshotInfo{}, backend.ErrNotFound
	}
	// The image a fork runs is the snapshot itself; the settings are the
	// original's. A sandbox started from a snapshot was itself started from
	// one: its own snapshot image holds them.
	src := sp.Image
	if sp.FromSnapshot != "" {
		src = snapshotRef(b.cfg.Owner, sp.FromSnapshot)
	}
	ic, err := b.configOf(ctx, src)
	if err != nil {
		return backend.SnapshotInfo{}, err
	}
	// The export is the size of the sandbox's files, and loading it copies
	// it once more into the runtime's store: on a nearly full disk it would
	// fail after a minute or two, leaving a partial image behind. Refused up
	// front below a floor, and again once the export's size is known.
	if err := b.roomFor(minSnapshotRoom); err != nil {
		return backend.SnapshotInfo{}, err
	}
	f, err := os.CreateTemp(b.cfg.ScratchDir, "snapshot-*.tar")
	if err != nil {
		return backend.SnapshotInfo{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	export := exec.CommandContext(ctx, b.cfg.Container, "export", id)
	var stderr bytes.Buffer
	export.Stderr = &stderr
	out, err := export.StdoutPipe()
	if err != nil {
		return backend.SnapshotInfo{}, err
	}
	if err := export.Start(); err != nil {
		return backend.SnapshotInfo{}, err
	}
	ref := snapshotRef(b.cfg.Owner, snapshotID)
	n, werr := writeImageArchive(f, out, ic, ref, map[string]string{LabelManaged: "1", LabelOwner: b.cfg.Owner})
	if werr != nil {
		_ = export.Process.Kill()
		_ = export.Wait()
		if errors.Is(werr, syscall.ENOSPC) {
			return backend.SnapshotInfo{}, fmt.Errorf("%w: the disk filled while the snapshot was written", backend.ErrNoSpace)
		}
		return backend.SnapshotInfo{}, werr
	}
	if err := export.Wait(); err != nil {
		return backend.SnapshotInfo{}, fmt.Errorf("container export: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := f.Close(); err != nil {
		return backend.SnapshotInfo{}, err
	}
	// The load copies the archive into the store; the archive is removed
	// only after it.
	if err := b.roomFor(n + loadMargin); err != nil {
		return backend.SnapshotInfo{}, err
	}
	if _, err := b.cli(ctx, "image", "load", "--input", f.Name()); err != nil {
		// A load that failed part way may have left an image no snapshot
		// will name: removed now, not left for the next restart.
		_, _ = b.cli(context.Background(), "image", "delete", "--force", ref)
		if strings.Contains(strings.ToLower(err.Error()), "no space left") {
			return backend.SnapshotInfo{}, fmt.Errorf("%w: the disk filled while the snapshot was loaded", backend.ErrNoSpace)
		}
		return backend.SnapshotInfo{}, err
	}
	b.mu.Lock()
	b.snaps[snapshotID] = ref
	b.mu.Unlock()
	return backend.SnapshotInfo{
		Kind: api.SnapshotDisk, ID: snapshotID, Bytes: n, Image: sp.Image,
		CPUs: sp.CPUs, MemoryMB: sp.MemoryMB, DiskMB: sp.DiskMB,
	}, nil
}

// DeleteSnapshot removes the snapshot's image. Sandboxes already started
// from it keep running: the runtime holds what they use.
func (b *Backend) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	b.mu.Lock()
	ref, ok := b.snaps[snapshotID]
	delete(b.snaps, snapshotID)
	b.mu.Unlock()
	if !ok {
		return backend.ErrNotFound
	}
	_, err := b.cli(ctx, "image", "delete", "--force", ref)
	return err
}

const (
	// minSnapshotRoom is the least free disk a snapshot is started with: a
	// sandbox's files are rarely less, the base image alone being 2 GB.
	minSnapshotRoom = 1 << 30
	// loadMargin is room beyond the archive's size for the load's copy.
	loadMargin = 512 << 20
)

// roomFor refuses when the scratch directory's filesystem has less than need
// bytes free. Unknown free space (another system, statfs failing) is not a
// refusal: the snapshot then fails, if it does, on the disk itself.
func (b *Backend) roomFor(need int64) error {
	dir := b.cfg.ScratchDir
	if dir == "" {
		dir = os.TempDir()
	}
	free, err := b.cfg.FreeBytes(dir)
	if err != nil || free < 0 || free >= need {
		return nil
	}
	return fmt.Errorf("%w: %s free, about %s needed for this snapshot", backend.ErrNoSpace, gib(free), gib(need))
}

func gib(n int64) string { return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30)) }

// snapshotImage is the image a sandbox started from a snapshot runs.
func (b *Backend) snapshotImage(snapshotID string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ref, ok := b.snaps[snapshotID]
	if !ok {
		return "", errors.New("no such snapshot")
	}
	return ref, nil
}

// reapSnapshots removes the snapshot images an earlier run of this sandboxd
// left, which nothing can name any more. Only its own: the owner is in the
// name.
func (b *Backend) reapSnapshots() {
	out, err := b.cli(context.Background(), "image", "ls", "--format", "json")
	if err != nil {
		return
	}
	var list []struct {
		Configuration struct {
			Name string `json:"name"`
		} `json:"configuration"`
	}
	if json.Unmarshal([]byte(out), &list) != nil {
		return
	}
	// The runtime may report the name with a registry in front
	// (docker.io/sbx-snapshot/…), so the path is matched, not the start.
	mine := snapshotRepo + "/" + b.cfg.Owner + "/"
	for _, img := range list {
		name := img.Configuration.Name
		if i := strings.Index(name, mine); i == 0 || (i > 0 && name[i-1] == '/') {
			_, _ = b.cli(context.Background(), "image", "delete", "--force", name)
			b.cfg.Logf("removed a snapshot left by an earlier run: %s", name)
		}
	}
}
