//go:build !linux

package deployment

import (
	"fmt"
	"os"
)

func lockDestination(string) (*os.File, error) {
	return nil, fmt.Errorf("deployment locking requires Linux")
}
