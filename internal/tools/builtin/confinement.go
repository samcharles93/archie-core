// Not lifted from tau: archie-original. It exports the read tool's own
// path policy so a tool outside this package cannot end up applying a
// second, subtly different one.
package builtin

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrPathNotAllowed reports a path refused by the workspace confinement.
var ErrPathNotAllowed = errors.New("path is outside the configured workspace")

// ResolveReadable resolves path as the read tool does and returns it when
// the read tool would allow it.
func ResolveReadable(cwd, path string) (string, error) {
	resolved := resolvePath(cwd, path)
	if !isReadConfined(cwd, resolved) {
		return "", fmt.Errorf("%w: %s", ErrPathNotAllowed, resolved)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", resolved, err)
	}
	return abs, nil
}
