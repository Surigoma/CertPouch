package deployment

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockDestination(destination string) (*os.File, error) {
	path := filepath.Join(destination, ".pull-guard")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		file.Close()
		return nil, fmt.Errorf("invalid deployment guard")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire deployment guard: %w", err)
	}
	return file, nil
}
