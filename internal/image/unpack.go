// Package image turns an OCI image into what a backend boots: on Linux, a
// read-only ext4 root disk for Firecracker. (The macOS runtime pulls OCI images
// itself.)
//
// Layers come from a registry and the image is named by the request, so a layer
// is untrusted input being written to the host's disk — as root, on a
// self-hosted or cloud node. This file is the part that has to be right.
package image

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxSymlinkHops bounds symlink resolution, as the kernel does (ELOOP at 40).
const maxSymlinkHops = 40

// UnpackStats reports what an unpack skipped, so a caller can say so.
type UnpackStats struct {
	Files, Dirs, Symlinks, Hardlinks int
	SkippedDevices                   int // device nodes and FIFOs: never created
	// UnmappedOwners counts entries whose owner could not be set because this
	// process runs as root in a user namespace that has no mapping for it.
	UnmappedOwners int
	Bytes          int64 // regular file content written
}

// Unpack applies one layer (a tar stream, gzip-compressed or not) on top of
// root, with OCI whiteout semantics. maxBytes bounds the content written by
// this layer.
//
// Every path is resolved inside root, the way the guest kernel will resolve it
// once root is its /: symlinks are followed within root, ".." stops at root,
// and an absolute symlink target means root's own /. Nothing an entry names —
// directly or through a link planted by an earlier entry or layer — can land
// outside root. The final component of a path is never followed: an entry
// replaces whatever is there rather than writing through it.
func Unpack(root string, layer io.Reader, maxBytes int64, st *UnpackStats) error {
	r, err := maybeGunzip(layer)
	if err != nil {
		return err
	}
	tr := tar.NewReader(r)
	isRoot := os.Geteuid() == 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading layer: %w", err)
		}
		rel, err := cleanEntryName(h.Name)
		if err != nil {
			return err
		}
		if rel == "" {
			continue // the root itself
		}
		dir, base := path.Split(rel)

		// Whiteouts: ".wh.name" deletes name from lower layers; ".wh..wh..opq"
		// empties the directory of what lower layers put there.
		if base == ".wh..wh..opq" {
			parent, err := resolveDir(root, dir)
			if err != nil {
				return err
			}
			if err := emptyDir(parent); err != nil {
				return err
			}
			continue
		}
		if name, ok := strings.CutPrefix(base, ".wh."); ok {
			parent, err := resolveDir(root, dir)
			if err != nil {
				return err
			}
			if name == "" || name == "." || name == ".." {
				return fmt.Errorf("layer entry %q: malformed whiteout", h.Name)
			}
			if err := os.RemoveAll(filepath.Join(parent, name)); err != nil {
				return err
			}
			continue
		}

		parent, err := resolveDir(root, dir)
		if err != nil {
			return fmt.Errorf("layer entry %q: %w", h.Name, err)
		}
		target := filepath.Join(parent, base)
		mode := fs.FileMode(h.Mode) & (fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky)
		if !isRoot {
			mode &^= fs.ModeSetuid | fs.ModeSetgid
			if h.Typeflag == tar.TypeDir {
				mode |= 0o700 // later entries must still be writable into it
			}
		}

		switch h.Typeflag {
		case tar.TypeDir:
			if fi, err := os.Lstat(target); err == nil && !fi.IsDir() {
				if err := os.Remove(target); err != nil {
					return err
				}
			}
			if err := os.Mkdir(target, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return err
			}
			st.Dirs++
		case tar.TypeReg, tar.TypeRegA:
			if err := replace(target); err != nil {
				return err
			}
			if st.Bytes+h.Size > maxBytes {
				return fmt.Errorf("image exceeds %d bytes of content", maxBytes)
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
			if err != nil {
				return err
			}
			n, err := io.Copy(f, io.LimitReader(tr, h.Size))
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
			st.Bytes += n
			st.Files++
		case tar.TypeSymlink:
			if err := replace(target); err != nil {
				return err
			}
			// The link is stored as written. It is resolved within root when it
			// is used — by a later entry here, or by the guest at run time.
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
			st.Symlinks++
			continue // no mode or ownership on a link
		case tar.TypeLink:
			src, err := resolveLinkTarget(root, h.Linkname)
			if err != nil {
				return fmt.Errorf("layer entry %q: hard link: %w", h.Name, err)
			}
			if err := replace(target); err != nil {
				return err
			}
			if err := os.Link(src, target); err != nil {
				return err
			}
			st.Hardlinks++
			continue // shares the source's inode, mode and owner
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			// Never created, as root or not. A device node is a door into the host
			// kernel for anything on the host that later opens it, and the layer
			// chooses its numbers; the guest gets its devices from devtmpfs, which
			// sandbox-guestd mounts at boot.
			st.SkippedDevices++
			continue
		default:
			continue // xattr-only headers and the like carry no file
		}

		if isRoot {
			if err := os.Lchown(target, h.Uid, h.Gid); err != nil {
				// EINVAL is a uid or gid this user namespace cannot map. On a real
				// host as root it does not happen; inside a namespace the file
				// keeps the namespace's root as owner, and the count says so.
				if !errors.Is(err, errInvalid) {
					return err
				}
				st.UnmappedOwners++
			}
		}
		if err := os.Chmod(target, mode); err != nil {
			return err
		}
	}
}

// cleanEntryName normalises a tar entry name to a slash-separated path relative
// to the root, refusing one that climbs out.
func cleanEntryName(name string) (string, error) {
	n := strings.TrimPrefix(name, "./")
	n = strings.TrimLeft(n, "/")
	for _, el := range strings.Split(n, "/") {
		if el == ".." {
			return "", fmt.Errorf("layer entry %q climbs out of the image", name)
		}
	}
	if strings.ContainsRune(n, 0) {
		return "", fmt.Errorf("layer entry %q contains a NUL", name)
	}
	n = path.Clean("/" + n)[1:]
	return n, nil
}

// resolveDir resolves a directory path inside root, following symlinks within
// root, creating directories that do not exist yet, and refusing anything that
// is not a directory. The result is a host path under root.
func resolveDir(root, rel string) (string, error) {
	return resolve(root, rel, true, 0)
}

// resolveLinkTarget resolves a hard link's source inside root. It must exist and
// be a regular file — linking to a directory is not a thing, and linking to a
// device is how a layer would hand the guest a node it never declared.
func resolveLinkTarget(root, name string) (string, error) {
	rel, err := cleanEntryName(name)
	if err != nil {
		return "", err
	}
	dir, base := path.Split(rel)
	parent, err := resolve(root, dir, false, 0)
	if err != nil {
		return "", err
	}
	p := filepath.Join(parent, base)
	fi, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", name)
	}
	return p, nil
}

func resolve(root, rel string, create bool, hops int) (string, error) {
	cur := root
	parts := strings.Split(rel, "/")
	for i := 0; i < len(parts); i++ {
		el := parts[i]
		switch el {
		case "", ".":
			continue
		case "..":
			// Clamped at root, as the guest's kernel clamps at its /.
			if cur != root {
				cur = filepath.Dir(cur)
			}
			continue
		}
		next := filepath.Join(cur, el)
		fi, err := os.Lstat(next)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if !create {
				return "", err
			}
			if err := os.Mkdir(next, 0o755); err != nil {
				return "", err
			}
			cur = next
		case err != nil:
			return "", err
		case fi.Mode()&fs.ModeSymlink != 0:
			hops++
			if hops > maxSymlinkHops {
				return "", errors.New("too many levels of symbolic links")
			}
			link, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			// Splice the link's components in place of this one and carry on;
			// an absolute target restarts from root.
			rest := append(strings.Split(link, "/"), parts[i+1:]...)
			if strings.HasPrefix(link, "/") {
				cur = root
			}
			parts, i = rest, -1
		case fi.IsDir():
			cur = next
		default:
			return "", fmt.Errorf("%s is not a directory", strings.TrimPrefix(next, root))
		}
	}
	return cur, nil
}

// replace removes whatever is at target unless it is a directory: a file entry
// replaces a file or link, it never writes through a link.
func replace(target string) error {
	fi, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

func emptyDir(dir string) error {
	des, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, de := range des {
		if err := os.RemoveAll(filepath.Join(dir, de.Name())); err != nil {
			return err
		}
	}
	return nil
}

func maybeGunzip(r io.Reader) (io.Reader, error) {
	br := &peekReader{r: r}
	head, err := br.peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		return gzip.NewReader(br)
	}
	return br, nil
}

type peekReader struct {
	r   io.Reader
	buf []byte
}

func (p *peekReader) peek(n int) ([]byte, error) {
	for len(p.buf) < n {
		b := make([]byte, n-len(p.buf))
		k, err := p.r.Read(b)
		p.buf = append(p.buf, b[:k]...)
		if err != nil {
			return p.buf, err
		}
	}
	return p.buf[:n], nil
}

func (p *peekReader) Read(b []byte) (int, error) {
	if len(p.buf) > 0 {
		n := copy(b, p.buf)
		p.buf = p.buf[n:]
		return n, nil
	}
	return p.r.Read(b)
}
