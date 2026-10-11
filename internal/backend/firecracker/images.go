//go:build linux

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// What an installed image is here: a root disk under ImageDir built for this
// sandboxd's guest agent, which records the references it serves
// (image.ListInstalled). Its layers are in the puller's blob cache, shared
// with any other image that has them.

// blobGrace is how new a blob may be and still be left by a removal: a
// create may have pulled it and not yet recorded the disk it builds.
const blobGrace = 10 * time.Minute

var _ backend.ImageStore = (*Backend)(nil)

// Images is backend.ImageStore.
func (b *Backend) Images(context.Context) ([]backend.ImageInfo, error) {
	list, err := image.ListInstalled(b.cfg.ImageDir, b.cfg.Agent)
	if err != nil {
		return nil, err
	}
	var out []backend.ImageInfo
	for _, in := range list {
		for _, ref := range in.Refs {
			out = append(out, backend.ImageInfo{Ref: ref, Digest: in.Digest, Bytes: in.Bytes, InstalledAt: in.BuiltAt})
		}
	}
	return out, nil
}

// InstallImage is backend.ImageStore: the pull and build a create does, ahead
// of one.
func (b *Backend) InstallImage(ctx context.Context, ref string, progress func(backend.ImageProgress)) error {
	if progress != nil {
		ctx = image.WithProgress(ctx, func(p image.Progress) {
			progress(backend.ImageProgress{Phase: p.Phase, Done: p.Done, Total: p.Total})
		})
	}
	rootfs, err := image.BuildRootFS(ctx, b.cfg.Puller, ref, b.cfg.Agent, b.cfg.ImageDir)
	if err != nil {
		return err
	}
	b.mu.Lock()
	if b.built == nil {
		b.built = map[string]bool{}
	}
	b.built[ref] = true
	b.mu.Unlock()
	if rootfs.OwnedByHost {
		b.cfg.Logf("image %s was built without root, so its files are owned by the building user", ref)
	}
	return nil
}

// RemoveImage is backend.ImageStore. The disk goes only with the last
// reference it serves, and never while a VM here — running or suspended —
// was started from one of them: without the jailer a VM reads the cached
// disk itself, and a suspended one is resumed from it. Then the blobs no
// installed image still needs.
func (b *Backend) RemoveImage(_ context.Context, ref string) (int64, error) {
	list, err := image.ListInstalled(b.cfg.ImageDir, b.cfg.Agent)
	if err != nil {
		return 0, err
	}
	var refs []string
	for _, in := range list {
		if slices.Contains(in.Refs, ref) {
			refs = in.Refs
		}
	}
	if refs == nil {
		return 0, backend.ErrNotFound
	}
	inUse := func(string) bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, v := range b.vms {
			if slices.Contains(refs, v.spec.Image) {
				return true
			}
		}
		return false
	}
	freed, err := image.RemoveInstalled(b.cfg.ImageDir, b.cfg.Agent, ref, inUse)
	switch {
	case errors.Is(err, image.ErrNotInstalled):
		return 0, backend.ErrNotFound
	case errors.Is(err, image.ErrInUse):
		return 0, fmt.Errorf("%w: a sandbox here starts from %s", backend.ErrBusy, ref)
	case err != nil:
		return 0, err
	}
	b.mu.Lock()
	delete(b.built, ref)
	b.mu.Unlock()
	_, blobs, err := image.RemoveUnusedBlobs(b.cfg.Puller.Cache, b.cfg.ImageDir, blobGrace)
	if err != nil {
		b.cfg.Logf("image %s: removing unused layers: %v", ref, err)
	}
	return freed + blobs, nil
}
