package image

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
)

func needMkfs(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/usr/sbin/mkfs.ext4"); err != nil {
		if _, err2 := os.Stat("/sbin/mkfs.ext4"); err2 != nil {
			t.Skip("mkfs.ext4 not available")
		}
	}
}

// A built disk says which references it serves, so the list survives the
// process that built it; a second tag of the same image shares the disk; and
// removing an image removes the disk only once no tag is left, and then the
// blobs no other disk needs.
func TestInstalledImagesAreRecorded(t *testing.T) {
	needMkfs(t)
	r, md, _ := newRegistry(t, "amd64")
	r.manifests["2.0"], r.types["2.0"] = r.manifests["1.0"], r.types["1.0"]
	p, host := puller(t, r)
	agent := filepath.Join(t.TempDir(), "agent")
	os.WriteFile(agent, []byte("#!/bin/sh\n"), 0o755)
	dir := t.TempDir()
	one, two := host+"/team/app:1.0", host+"/team/app:2.0"

	var mu sync.Mutex
	var seen []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { mu.Lock(); seen = append(seen, p); mu.Unlock() })
	fs1, err := BuildRootFS(ctx, p, one, agent, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || seen[len(seen)-1].Phase != "building" {
		t.Fatalf("progress %v; want pulling, then building", seen)
	}
	pulled := seen[len(seen)-2]
	if pulled.Total == 0 || pulled.Done != pulled.Total {
		t.Errorf("the pull ended at %d of %d bytes", pulled.Done, pulled.Total)
	}
	if _, err := BuildRootFS(context.Background(), p, two, agent, dir); err != nil {
		t.Fatal(err)
	}

	// What a restart sees: the records on the filesystem, and nothing else.
	list, err := ListInstalled(dir, agent)
	if err != nil || len(list) != 1 {
		t.Fatalf("listed %v, %v; want one disk", list, err)
	}
	if !slices.Equal(list[0].Refs, []string{one, two}) || list[0].Digest != md || list[0].Bytes == 0 || list[0].Path != fs1.Path {
		t.Fatalf("listed %+v", list[0])
	}

	// A disk a sandbox was booted from stays.
	if _, err := RemoveInstalled(dir, agent, one, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveInstalled(dir, agent, two, func(string) bool { return true }); !errors.Is(err, ErrInUse) {
		t.Fatalf("removing the last tag of a disk in use: %v", err)
	}
	if _, err := os.Stat(fs1.Path); err != nil {
		t.Fatalf("the disk went while another tag still served it: %v", err)
	}
	freed, err := RemoveInstalled(dir, agent, two, nil)
	if err != nil || freed == 0 {
		t.Fatalf("removing the last tag: freed %d, %v", freed, err)
	}
	if _, err := os.Stat(filepath.Dir(fs1.Path)); !os.IsNotExist(err) {
		t.Fatalf("the disk outlived its last tag: %v", err)
	}
	if _, err := RemoveInstalled(dir, agent, two, nil); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("removing it again: %v", err)
	}

	// Its blobs go once nothing names them, but not ones written just now.
	if n, _, _ := RemoveUnusedBlobs(p.Cache, dir, time.Hour); n != 0 {
		t.Fatalf("removed %d fresh blobs", n)
	}
	n, freedBlobs, err := RemoveUnusedBlobs(p.Cache, dir, 0)
	if err != nil || n != 2 || freedBlobs == 0 {
		t.Fatalf("removed %d blobs (%d bytes), %v; want the config and the layer", n, freedBlobs, err)
	}
}

// A disk with no record — from before records — makes every blob's use
// unknown, so none is removed.
func TestUnusedBlobsWaitForEveryRecord(t *testing.T) {
	cache, dir := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "rootfs", "oldkey"), 0o755)
	blobs := filepath.Join(cache, "blobs", "sha256")
	os.MkdirAll(blobs, 0o700)
	os.WriteFile(filepath.Join(blobs, "abc"), []byte("x"), 0o600)
	if n, _, err := RemoveUnusedBlobs(cache, dir, 0); err != nil || n != 0 {
		t.Fatalf("removed %d, %v; want none while a disk's blobs are unknown", n, err)
	}
}
