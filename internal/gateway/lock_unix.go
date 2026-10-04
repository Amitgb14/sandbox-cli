//go:build unix

package gateway

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type fileLock struct{ f *os.File }

// lockFile takes an exclusive, non-blocking lock on path. A second process
// is refused at once rather than left waiting behind a server that never
// lets go.
func lockFile(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("another process holds %s: change the state of a serving gateway through its admin API, or stop it first", path)
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) unlock() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
