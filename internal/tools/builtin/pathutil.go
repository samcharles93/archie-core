package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// maxReadBytes is the maximum file size the read tool will load into memory.
	maxReadBytes = 5 * 1024 * 1024 // 5MB

	// maxWriteBytes is the maximum content size the write tool will accept.
	maxWriteBytes = 5 * 1024 * 1024 // 5MB
)

// resolvePath resolves a potentially relative path against the working directory.
// It also strips a leading @ (some LLMs include this).
func resolvePath(cwd, path string) string {
	path = strings.TrimPrefix(path, "@")
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

// goModCacheDir returns the Go module cache root, or "".
var goModCacheDir = sync.OnceValue(func() string {
	if v := os.Getenv("GOMODCACHE"); v != "" {
		return filepath.Clean(v)
	}
	if v := os.Getenv("GOPATH"); v != "" {
		// GOPATH may be a list; cmd/go uses the first entry.
		if first, _, _ := strings.Cut(v, string(os.PathListSeparator)); first != "" {
			return filepath.Join(filepath.Clean(first), "pkg", "mod")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "go", "pkg", "mod")
})

// isReadConfined reports whether a read may touch target: the workspace or
// the Go module cache.
func isReadConfined(base, target string) bool {
	if isConfined(base, target) {
		return true
	}
	cache := goModCacheDir()
	return cache != "" && isConfined(cache, target)
}

// pathConfinement gates the workspace jail on the file tools. On by default.
var pathConfinement atomic.Bool

func init() { pathConfinement.Store(true) }

// SetPathConfinement turns the workspace jail on the read, write, edit, find
// and grep tools on or off. Relative paths always resolve against the
// workspace.
func SetPathConfinement(enabled bool) { pathConfinement.Store(enabled) }

// isConfined checks whether target is within (or equal to) the base directory.
// Returns false if target escapes via ../ or is an unrelated absolute path.
func isConfined(base, target string) bool {
	if !pathConfinement.Load() {
		return true // confinement disabled by configuration
	}
	if base == "" {
		return true // no confinement if cwd is unset
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}
