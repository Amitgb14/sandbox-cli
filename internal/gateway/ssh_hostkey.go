package gateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// loadHostKey reads the gateway's ed25519 host key from path, creating it
// when the file does not exist.
//
// The host key is what every client pins (GET /v1/ssh publishes it), so
// whoever can read it can impersonate the gateway to every user of the fleet.
// A key file anyone but its owner can read is therefore refused, not
// tightened in place: a chmod would hide that it was exposed until now. A
// symlink is refused too, so the key cannot be redirected to a file someone
// else controls. Only ed25519 is accepted: one key type, the one OpenSSH
// prefers, and nothing to decide about RSA sizes.
func loadHostKey(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("ssh: a host key file is required")
	}
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return createHostKey(path)
	case err != nil:
		return nil, fmt.Errorf("ssh: host key: %w", err)
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("ssh: host key %s is not a regular file", path)
	case fi.Mode().Perm()&0o077 != 0:
		return nil, fmt.Errorf("ssh: host key %s is readable by others (mode %04o); it must be 0600 — replace the key, since it may have been read", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ssh: host key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		// The parse error never carries key material.
		return nil, fmt.Errorf("ssh: host key %s: %w", path, err)
	}
	if t := signer.PublicKey().Type(); t != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("ssh: host key %s is %s; the gateway uses ed25519", path, t)
	}
	return signer, nil
}

// createHostKey generates an ed25519 key and writes it 0600 with O_EXCL, so
// two gateways starting at once cannot each write a key and leave clients
// pinned to the one that lost.
func createHostKey(path string) (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ssh: generating a host key: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "sandbox-gateway host key")
	if err != nil {
		return nil, fmt.Errorf("ssh: encoding the host key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("ssh: host key directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return loadHostKey(path)
		}
		return nil, fmt.Errorf("ssh: writing the host key: %w", err)
	}
	if err := pem.Encode(f, block); err != nil {
		f.Close()
		os.Remove(path)
		return nil, fmt.Errorf("ssh: writing the host key: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return nil, fmt.Errorf("ssh: writing the host key: %w", err)
	}
	return ssh.NewSignerFromKey(priv)
}
