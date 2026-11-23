package storage

import (
	"fmt"
	"os"
	"syscall"
)

func publicationOwnership(info os.FileInfo) (storeOwnership, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return storeOwnership{}, fmt.Errorf("certificate store ownership is unavailable")
	}
	return storeOwnership{uid: int(stat.Uid), gid: int(stat.Gid)}, nil
}
