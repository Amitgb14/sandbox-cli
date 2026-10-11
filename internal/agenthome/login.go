package agenthome

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The agent's login lives in ~/.config/sandbox/agents/<agent>/ on the host,
// one file per AuthPath, 0600. It is copied in when a run starts and back out
// when it ends — the host never mounts anything into the guest for it.

func authDir(d agents.Descriptor) string { return LoginDir(d) }

// LoginDir is where an agent's saved login lives on the host: the files named
// by its AuthPaths, copied out of the last sandbox that ran it.
func LoginDir(d agents.Descriptor) string {
	return filepath.Join(ConfigDir(), "agents", d.PersistDir)
}

// GuestHome is the sandbox user's HOME in the base image.
const GuestHome = "/sandbox/home"

// RestoreLogin copies an agent's saved login into a sandbox.
func RestoreLogin(ctx context.Context, c *api.Client, sandbox string, d agents.Descriptor) {
	for _, rel := range d.AuthPaths {
		data, err := os.ReadFile(filepath.Join(authDir(d), filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		// Filtered here too: a file saved before the filter existed may
		// carry what it now drops (agents.FilterAuth).
		if data, ok := agents.FilterAuth(rel, data); ok {
			_ = c.WriteFile(ctx, sandbox, GuestHome+"/"+rel, data)
		}
	}
}

// SaveLogin copies an agent's login out of a sandbox, to restore next time.
func SaveLogin(ctx context.Context, c *api.Client, sandbox string, d agents.Descriptor) {
	for _, rel := range d.AuthPaths {
		data, err := c.ReadFile(ctx, sandbox, GuestHome+"/"+rel)
		if err != nil {
			continue
		}
		data, ok := agents.FilterAuth(rel, data)
		if !ok {
			fmt.Fprintf(os.Stderr, "sandbox-cli: not saving the %s login's %s: it is not the JSON object it should be\n", d.Name, rel)
			continue
		}
		if err := WritePrivate(authDir(d), rel, data); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-cli: saving the %s login: %v\n", d.Name, err)
		}
	}
}

// WritePrivate writes rel under dir, which sandbox-cli owns: owner-only, and
// never through a symlink — the content came from a guest, and a link planted
// in this directory must not redirect the write.
func WritePrivate(dir, rel string, data []byte) error {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		return errors.New("path escapes the login directory")
	}
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; not writing through it", d)
		}
		if d == dir || len(d) <= len(dir) {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; not writing through it", path)
	}
	// The temporary file is made fresh, never opened as found: WriteFile
	// follows a link planted at path.tmp, and keeps the mode of a file
	// already there — a leftover 0644 one made the login, or a saved API
	// key, readable by every user once renamed into place. O_EXCL refuses
	// both, a link included.
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
