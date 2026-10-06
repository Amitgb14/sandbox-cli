package macos

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// smallRootfs is a filesystem export in miniature: a directory and a file.
func smallRootfs(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "sandbox/home/", Typeflag: tar.TypeDir, Mode: 0o755})
	data := []byte("prepared\n")
	_ = tw.WriteHeader(&tar.Header{Name: "sandbox/home/state.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(data))})
	_, _ = tw.Write(data)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The archive is a valid OCI layout that names the image: every blob is the
// content its digest says, the layer is the export byte for byte, and the
// config carries the original image's settings and the owner's labels. The
// layer's header is written into the room left for it, so the archive reads
// from its first byte.
func TestWriteImageArchive(t *testing.T) {
	layer := smallRootfs(t)
	f, err := os.CreateTemp(t.TempDir(), "img-*.tar")
	if err != nil {
		t.Fatal(err)
	}
	ic := imageConfig{Architecture: "arm64", OS: "linux", Env: []string{"PATH=/usr/bin", "HOME=/sandbox/home"}, WorkingDir: "/sandbox/home"}
	ref := snapshotRef("ownerhash", "snp_0123")
	n, err := writeImageArchive(f, bytes.NewReader(layer), ic, ref, map[string]string{LabelManaged: "1", LabelOwner: "ownerhash"})
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(layer)) {
		t.Fatalf("layer size %d, want %d", n, len(layer))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	var order []string
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the archive does not read: %v", err)
		}
		b, _ := io.ReadAll(tr)
		entries[h.Name] = b
		order = append(order, h.Name)
	}
	f.Close()
	for name, b := range entries {
		if d, ok := strings.CutPrefix(name, "blobs/sha256/"); ok {
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != d {
				t.Errorf("blob %s is not the content its digest names", d)
			}
		}
	}
	if !strings.HasPrefix(order[0], "blobs/sha256/") || !bytes.Equal(entries[order[0]], layer) {
		t.Fatalf("the first entry is not the layer, byte for byte: %s", order[0])
	}

	var index struct {
		Manifests []struct {
			Digest      string            `json:"digest"`
			Annotations map[string]string `json:"annotations"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(entries["index.json"], &index); err != nil || len(index.Manifests) != 1 {
		t.Fatalf("index.json: %v %s", err, entries["index.json"])
	}
	if index.Manifests[0].Annotations["org.opencontainers.image.ref.name"] != ref {
		t.Errorf("the image is not named %s: %v", ref, index.Manifests[0].Annotations)
	}
	var manifest struct {
		Config struct{ Digest string } `json:"config"`
		Layers []struct {
			Digest string
			Size   int64
		} `json:"layers"`
	}
	_ = json.Unmarshal(entries["blobs/sha256/"+strings.TrimPrefix(index.Manifests[0].Digest, "sha256:")], &manifest)
	if len(manifest.Layers) != 1 || manifest.Layers[0].Digest != "sha256:"+strings.TrimPrefix(order[0], "blobs/sha256/") || manifest.Layers[0].Size != n {
		t.Fatalf("manifest layers %+v", manifest.Layers)
	}
	var cfg struct {
		Config struct {
			Env        []string
			WorkingDir string
			Labels     map[string]string
		} `json:"config"`
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}
	_ = json.Unmarshal(entries["blobs/sha256/"+strings.TrimPrefix(manifest.Config.Digest, "sha256:")], &cfg)
	if strings.Join(cfg.Config.Env, ",") != "PATH=/usr/bin,HOME=/sandbox/home" || cfg.Config.WorkingDir != "/sandbox/home" {
		t.Errorf("the original's settings were not kept: %+v", cfg.Config)
	}
	if cfg.Config.Labels[LabelOwner] != "ownerhash" || cfg.Config.Labels[LabelManaged] != "1" {
		t.Errorf("labels %v", cfg.Config.Labels)
	}
	if len(cfg.Rootfs.DiffIDs) != 1 || cfg.Rootfs.DiffIDs[0] != manifest.Layers[0].Digest {
		t.Errorf("diff_ids %v; want the layer's digest", cfg.Rootfs.DiffIDs)
	}
	if string(entries["oci-layout"]) != `{"imageLayoutVersion":"1.0.0"}` {
		t.Errorf("oci-layout %q", entries["oci-layout"])
	}
}

// The backend's snapshot, against a stand-in for the runtime: it exports the
// sandbox, keeps the original image's settings, loads the result under its
// owner's name, runs that image for a fork, and deletes it with the snapshot.
// On start it removes this owner's leftover snapshot images and no one else's.
func TestSnapshotThroughTheRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script stands in for the runtime")
	}
	work := t.TempDir()
	calls := filepath.Join(work, "calls")
	rootfs := filepath.Join(work, "rootfs.tar")
	if err := os.WriteFile(rootfs, smallRootfs(t), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded := filepath.Join(work, "loaded.tar")
	script := filepath.Join(work, "container")
	os.WriteFile(script, []byte(`#!/bin/sh
echo "$*" >> "`+calls+`"
case "$1 $2" in
"ls --all") echo '[]' ;;
"image ls") echo '[{"configuration":{"name":"docker.io/sbx-snapshot/me/snp_old:latest"}},{"configuration":{"name":"sbx-snapshot/other/snp_theirs:latest"}},{"configuration":{"name":"docker.io/library/alpine:3.20"}}]' ;;
"image inspect") echo '[{"variants":[{"platform":{"os":"linux","architecture":"amd64"},"config":{"architecture":"amd64","os":"linux","config":{"Env":["WRONG=1"]}}},{"platform":{"os":"linux","architecture":"arm64"},"config":{"architecture":"arm64","os":"linux","config":{"Env":["PATH=/usr/bin"],"WorkingDir":"/sandbox/home"}}}]}]' ;;
"image load") cp "$4" "`+loaded+`" ;;
esac
case "$1" in
export) cat "`+rootfs+`" ;;
run) exit 1 ;;
esac
`), 0o755)
	agent := filepath.Join(work, "sandbox-guestd")
	os.WriteFile(agent, []byte("x"), 0o755)
	be, err := New(Config{Container: script, Agent: agent, Owner: "me", ScratchDir: filepath.Join(work, "scratch"), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if !be.Capabilities()[api.CapDiskSnapshot] || be.Capabilities()[api.CapMemorySnapshot] {
		t.Fatalf("capabilities %v; want disk_snapshot and not memory_snapshot", be.Capabilities())
	}
	be.specs["sbx_1"] = backend.Spec{ID: "sbx_1", Image: "base:1", CPUs: 1, MemoryMB: 1024, DiskMB: 10240}

	info, err := be.Snapshot(context.Background(), "sbx_1", "snp_new")
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != api.SnapshotDisk || info.Image != "base:1" || info.MemoryMB != 1024 || info.Bytes == 0 {
		t.Fatalf("snapshot info %+v", info)
	}
	if entries, _ := os.ReadDir(filepath.Join(work, "scratch")); len(entries) != 0 {
		t.Errorf("the archive was left behind: %v", entries)
	}
	got, _ := os.ReadFile(loaded)
	if !bytes.Contains(got, []byte("prepared")) || !bytes.Contains(got, []byte(`"PATH=/usr/bin"`)) || bytes.Contains(got, []byte("WRONG=1")) {
		t.Fatal("the loaded image lacks the export or the arm64 settings, or took another platform's")
	}

	_ = be.Create(context.Background(), backend.Spec{ID: "sbx_2", Image: "base:1", FromSnapshot: "snp_new", CPUs: 1, MemoryMB: 2048})
	if err := be.DeleteSnapshot(context.Background(), "snp_new"); err != nil {
		t.Fatal(err)
	}
	if err := be.DeleteSnapshot(context.Background(), "snp_new"); err != backend.ErrNotFound {
		t.Errorf("a second delete: %v; want not found", err)
	}

	log, _ := os.ReadFile(calls)
	s := string(log)
	for _, want := range []string{
		"image delete --force docker.io/sbx-snapshot/me/snp_old:latest", // this owner's leftover, at start
		"export sbx_1",
		"image inspect base:1",
		"image load --input ",
		"-- sbx-snapshot/me/snp_new:latest /.sbx/sandbox-guestd idle", // the fork runs the snapshot
		"image delete --force sbx-snapshot/me/snp_new:latest",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in the runtime's calls:\n%s", want, s)
		}
	}
	if strings.Contains(s, "snp_theirs") || strings.Contains(s, "delete --force docker.io/library/alpine") {
		t.Errorf("an image this sandboxd did not make was touched:\n%s", s)
	}
}

// The layer's header fills exactly the room left for it, for a small layer
// and for one past 8 GiB, where USTAR cannot say the size; and a reader takes
// the size from it.
func TestLayerHeaderFitsItsRoomAtAnySize(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	for _, size := range []int64{0, 9, 9 << 30, 1 << 40} {
		h, err := layerHeader(digest, size)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		hdr, err := tar.NewReader(bytes.NewReader(append(h, make([]byte, 1024)...))).Next()
		if err != nil || hdr.Size != size || hdr.Name != "blobs/sha256/"+digest {
			t.Fatalf("size %d: read back %+v, %v", size, hdr, err)
		}
	}
}
