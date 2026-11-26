//go:build !linux

package dns

import (
	"context"
	"fmt"
)

func RefreshCache(context.Context, string, int, func(context.Context) ([]byte, error)) (bool, error) {
	return false, fmt.Errorf("DNS cache refresh requires Linux for atomic replacement and permission checks")
}

func RefreshCacheAfter(ctx context.Context, path string, limit int, fetch func(context.Context) ([]byte, error), _ func(context.Context) error) (bool, error) {
	return RefreshCache(ctx, path, limit, fetch)
}
