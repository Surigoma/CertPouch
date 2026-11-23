//go:build !linux

package dns

import (
	"context"
	"fmt"
)

func RefreshCache(context.Context, string, int, func(context.Context) ([]byte, error)) (bool, error) {
	return false, fmt.Errorf("DNS cache refresh requires Linux for atomic replacement and permission checks")
}
