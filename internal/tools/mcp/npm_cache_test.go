package mcp

import (
	"slices"
	"testing"
)

func TestNpmCacheEnv(t *testing.T) {
	cases := []struct {
		name     string
		command  string
		cacheDir string
		want     []string
	}{
		{
			name:     "npx gets a persistent cache",
			command:  "npx",
			cacheDir: "/data/cache/mcp-npm",
			want: []string{
				"NPM_CONFIG_CACHE=/data/cache/mcp-npm",
				"NPM_CONFIG_PREFER_OFFLINE=true",
			},
		},
		{
			name:     "an absolute path to npx still gets the persistent cache",
			command:  "/usr/local/bin/npx",
			cacheDir: "/data/cache/mcp-npm",
			want: []string{
				"NPM_CONFIG_CACHE=/data/cache/mcp-npm",
				"NPM_CONFIG_PREFER_OFFLINE=true",
			},
		},
		{
			name:     "a non-npx command has no npm registry dependency to fix",
			command:  "/usr/local/bin/my-mcp-server",
			cacheDir: "/data/cache/mcp-npm",
			want:     nil,
		},
		{
			name:     "empty command is left alone",
			command:  "",
			cacheDir: "/data/cache/mcp-npm",
			want:     nil,
		},
		{
			name:     "empty cacheDir is refused rather than caching into the process CWD",
			command:  "npx",
			cacheDir: "",
			want:     nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NpmCacheEnv(c.command, c.cacheDir)
			if !slices.Equal(got, c.want) {
				t.Errorf("NpmCacheEnv(%q, %q) = %v, want %v", c.command, c.cacheDir, got, c.want)
			}
		})
	}
}
