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

// ResolveReadable resolves path the way the read tool does  --  relative
// against cwd, leading @ stripped  --  and refuses anything the read tool
// would refuse, returning the absolute path when it is allowed.
//
// Callers that hand a file to somewhere outside the process (an outbound
// attachment, say) must gate on this rather than on their own check: the
// jail is a deployment posture, and a second implementation of it is a
// second thing to keep in step with SetPathConfinement.
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
