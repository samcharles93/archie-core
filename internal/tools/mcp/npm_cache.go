package mcp

import "path/filepath"

// NpmCacheEnv returns env vars pointing npx's package cache at cacheDir. Empty
// unless command's base name is npx and cacheDir is set.
func NpmCacheEnv(command, cacheDir string) []string {
	if filepath.Base(command) != "npx" || cacheDir == "" {
		return nil
	}
	return []string{
		"NPM_CONFIG_CACHE=" + cacheDir,
		"NPM_CONFIG_PREFER_OFFLINE=true",
	}
}
