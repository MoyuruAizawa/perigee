package lock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock files remain on disk so another process cannot lock a replacement inode.
// The kernel releases flock after normal shutdown and after SIGKILL.
func Acquire(owner string) (func(), error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "perigee", "locks")
	return acquireAt(dir, owner)
}

func acquireAt(dir, owner string) (func(), error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, owner+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another Perigee process owns this runner pool: %w", err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
