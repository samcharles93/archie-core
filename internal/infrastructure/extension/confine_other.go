//go:build !linux

package extension

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

// confinedCommand refuses: extensions run only where their egress can be
// confined to the hosts the operator accepted.
func confinedCommand(context.Context, *os.File, string) (*exec.Cmd, error) {
	return nil, errors.New("extensions need Linux network namespaces to confine their egress")
}

func openCommand(ctx context.Context, binary *os.File) *exec.Cmd {
	return exec.CommandContext(ctx, binary.Name()) // the host-verified binary
}
