//go:build !linux

package storage

import (
	"fmt"
	"os"
)

func publicationOwnership(os.FileInfo) (storeOwnership, error) {
	return storeOwnership{}, fmt.Errorf("certificate publication requires Linux")
}
