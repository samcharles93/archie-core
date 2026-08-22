package mcp

import "path/filepath"

// NpmCacheEnv returns environment variables that make an npx-launched MCP
// server reuse a persistent package cache at cacheDir, instead of npm
// re-resolving and re-downloading the package from the registry on every
// launch. Empty for any command other than npx, or an empty cacheDir --
// npx has a registry dependency other commands don't, and an empty cacheDir
// means the caller has no persistent location to point npm at.
//
// This covers the npm package itself, not a package's own separate
// download path -- e.g. Playwright/Puppeteer-based servers fetch browser
// binaries from a CDN via a postinstall script, gated by their own env
// vars, which this does not touch.
//
// Matched by base name, not exact string: a command may be pinned via an
// absolute path (an operator's daemon config, or a container image's PATH),
// and an exact "npx" match would skip the fix silently, with no error
// logged, leaving the underlying registry re-fetch in place unnoticed.
func NpmCacheEnv(command, cacheDir string) []string {
	if filepath.Base(command) != "npx" || cacheDir == "" {
		return nil
	}
	return []string{
		"NPM_CONFIG_CACHE=" + cacheDir,
		"NPM_CONFIG_PREFER_OFFLINE=true",
	}
}
