//go:build !linux

package update

import (
	"context"
	"fmt"
)

func Apply(context.Context, string) (string, bool, error) {
	return "", false, fmt.Errorf("automatic installation is supported only on Linux")
}
