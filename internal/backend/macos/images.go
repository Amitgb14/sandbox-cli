package macos

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Images here are the runtime's: `container` keeps them in its own store,
// and a sandbox runs one as it is, with the guest agent mounted in rather
// than built into a disk. So installing one is the runtime's pull, with no
// build after it, and the runtime says what it holds. It does not say sizes
// or digests in what this backend reads, so those are left out.

var _ backend.ImageStore = (*Backend)(nil)

// Images is backend.ImageStore: the runtime's images, but for snapshots.
func (b *Backend) Images(context.Context) ([]backend.ImageInfo, error) {
	var out []backend.ImageInfo
	for _, ref := range b.CachedImages() {
		out = append(out, backend.ImageInfo{Ref: ref})
	}
	return out, nil
}

// InstallImage is backend.ImageStore: `container image pull`. The runtime
// reports no progress this backend reads, so it says only that it is
// pulling.
func (b *Backend) InstallImage(ctx context.Context, ref string, progress func(backend.ImageProgress)) error {
	if progress != nil {
		progress(backend.ImageProgress{Phase: "pulling"})
	}
	if _, err := b.cli(ctx, "image", "pull", ref); err != nil {
		return err
	}
	b.forgetImages()
	return nil
}

// RemoveImage is backend.ImageStore: `container image delete`, without
// --force, so the runtime refuses one a container uses too.
func (b *Backend) RemoveImage(ctx context.Context, ref string) (int64, error) {
	b.forgetImages()
	if !slices.Contains(b.CachedImages(), ref) {
		return 0, backend.ErrNotFound
	}
	b.mu.Lock()
	for _, s := range b.specs {
		if s.Image == ref {
			b.mu.Unlock()
			return 0, fmt.Errorf("%w: a sandbox starts from %s", backend.ErrBusy, ref)
		}
	}
	b.mu.Unlock()
	if _, err := b.cli(ctx, "image", "delete", ref); err != nil {
		return 0, err
	}
	b.forgetImages()
	return 0, nil
}

// forgetImages makes the next read of the image list ask the runtime.
func (b *Backend) forgetImages() {
	b.mu.Lock()
	b.imagesAt = time.Time{}
	b.mu.Unlock()
}
