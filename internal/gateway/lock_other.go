//go:build !unix

package gateway

import "os"

type fileLock struct{ f *os.File }

// lockFile on a platform without flock only opens the lock file. The gateway
// serves from Linux; elsewhere it builds for development.
func lockFile(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) unlock() { l.f.Close() }
