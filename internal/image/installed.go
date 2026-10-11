package image

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// A root disk is cached by a digest of image and guest agent, which names no
// reference, so nothing said which images were installed: the list lived in
// the backend's memory and a restart emptied it, with every disk still on
// the filesystem. Each disk now records, in recordFile beside it, the
// references it was built or reused for, the image's digest and the blobs it
// came from — so the installed images can be listed after a restart, and
// removing one knows which blobs another still needs.

const recordFile = "image.json"

// ErrNotInstalled is a reference no current root disk serves.
var ErrNotInstalled = errors.New("image not installed")

type diskRecord struct {
	Refs    []string  `json:"refs"`
	Digest  string    `json:"digest"`
	Blobs   []string  `json:"blobs"`
	BuiltAt time.Time `json:"built_at"`
}

// Installed is one root disk built for the current guest agent, and the
// references it serves.
type Installed struct {
	Refs    []string
	Digest  string
	Bytes   int64 // the root disk and its record, on the filesystem
	BuiltAt time.Time
	Path    string // the root disk
}

// recordRef notes that the disk in dir serves ref. A failure costs only the
// image being missing from the list until its next use, so it is not
// reported. Called with the disk's key lock held.
func recordRef(dir, ref string, pulled *Pulled) {
	var rec diskRecord
	if b, err := os.ReadFile(filepath.Join(dir, recordFile)); err == nil {
		_ = json.Unmarshal(b, &rec)
	}
	if rec.BuiltAt.IsZero() {
		rec.BuiltAt = time.Now().UTC()
	}
	if !slices.Contains(rec.Refs, ref) {
		rec.Refs = append(rec.Refs, ref)
		sort.Strings(rec.Refs)
	}
	rec.Digest, rec.Blobs = pulled.Digest, pulled.Blobs
	writeRecord(dir, rec)
}

func writeRecord(dir string, rec diskRecord) {
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "."+recordFile+".tmp")
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(dir, recordFile))
	}
}

func readRecord(dir string) (diskRecord, bool) {
	var rec diskRecord
	b, err := os.ReadFile(filepath.Join(dir, recordFile))
	if err != nil || json.Unmarshal(b, &rec) != nil {
		return rec, false
	}
	return rec, true
}

// ListInstalled is every root disk under dir built for agent that records
// the references it serves. A disk from before the record is left out until
// a sandbox next uses it, which records it.
func ListInstalled(dir, agent string) ([]Installed, error) {
	agentSum, err := fileSHA(agent)
	if err != nil {
		return nil, err
	}
	want := agentLabel(agentSum)
	des, err := os.ReadDir(filepath.Join(dir, "rootfs"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		d := filepath.Join(dir, "rootfs", de.Name())
		if label, err := os.ReadFile(filepath.Join(d, agentFile)); err != nil || string(label) != want {
			continue
		}
		disk := filepath.Join(d, "rootfs.ext4")
		if _, err := os.Stat(disk); err != nil {
			continue
		}
		rec, ok := readRecord(d)
		if !ok || len(rec.Refs) == 0 {
			continue
		}
		out = append(out, Installed{Refs: rec.Refs, Digest: rec.Digest, Bytes: dirBytes(d), BuiltAt: rec.BuiltAt, Path: disk})
	}
	return out, nil
}

// RemoveInstalled takes ref off the current agent's disk that serves it, and
// removes the disk when no reference is left: another tag of the same image
// keeps it. inUse reports a disk a VM was booted from, which is refused, as
// PruneRootFS keeps one. It returns the bytes freed.
func RemoveInstalled(dir, agent, ref string, inUse func(path string) bool) (int64, error) {
	list, err := ListInstalled(dir, agent)
	if err != nil {
		return 0, err
	}
	for _, in := range list {
		if !slices.Contains(in.Refs, ref) {
			continue
		}
		d := filepath.Dir(in.Path)
		unlock := lockKey(in.Path)
		defer unlock()
		rec, _ := readRecord(d)
		rec.Refs = slices.DeleteFunc(rec.Refs, func(r string) bool { return r == ref })
		if len(rec.Refs) > 0 {
			writeRecord(d, rec)
			return 0, nil
		}
		if inUse != nil && inUse(in.Path) {
			return 0, ErrInUse
		}
		size := dirBytes(d)
		if err := os.RemoveAll(d); err != nil {
			return 0, err
		}
		return size, nil
	}
	return 0, ErrNotInstalled
}

// ErrInUse is a root disk a sandbox was booted from.
var ErrInUse = errors.New("a sandbox runs from this image's disk")

// RemoveUnusedBlobs deletes the cached blobs under cache that no root disk's
// record names, leaving any written in the last minAge: a create may be
// between pulling a blob and recording the disk it builds from it. It does
// nothing while any disk under dir has no record, since that disk's blobs
// are unknown. It returns how many it removed and the bytes freed.
func RemoveUnusedBlobs(cache, dir string, minAge time.Duration) (removed int, freed int64, err error) {
	keep := map[string]bool{}
	des, err := os.ReadDir(filepath.Join(dir, "rootfs"))
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, err
	}
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		rec, ok := readRecord(filepath.Join(dir, "rootfs", de.Name()))
		if !ok {
			return 0, 0, nil
		}
		for _, b := range rec.Blobs {
			keep[strings.TrimPrefix(b, "sha256:")] = true
		}
	}
	blobs := filepath.Join(cache, "blobs", "sha256")
	bes, err := os.ReadDir(blobs)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	cutoff := time.Now().Add(-minAge)
	for _, be := range bes {
		if be.IsDir() || strings.HasPrefix(be.Name(), ".") || keep[be.Name()] {
			continue
		}
		fi, err := be.Info()
		if err != nil || fi.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(blobs, be.Name())) == nil {
			removed++
			freed += diskUsage(fi)
		}
	}
	return removed, freed, nil
}
