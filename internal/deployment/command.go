package deployment

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

const DefaultCommandTimeout = 30 * time.Second

// ConfigureCommand runs the operator-configured argv directly without a shell.
func ConfigureCommand(destination, owner, group, publicMode, keyMode string, args []string, timeout time.Duration) (Options, func(context.Context) error, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || timeout < 0 {
		return Options{}, nil, fmt.Errorf("configure an executable and a valid command timeout")
	}
	options, err := configureFiles(destination, owner, group, publicMode, keyMode)
	if err != nil {
		return Options{}, nil, err
	}
	if timeout == 0 {
		timeout = DefaultCommandTimeout
	}
	options.RecoveryTimeout = timeout
	args = slices.Clone(args)
	apply := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := exec.CommandContext(ctx, args[0], args[1:]...).Run(); err != nil {
			return fmt.Errorf("run deployment command: %w", errors.Join(ctx.Err(), err))
		}
		return nil
	}
	return options, apply, nil
}
