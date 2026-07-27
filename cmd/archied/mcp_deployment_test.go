package main

import (
	"os"
	"strings"
	"testing"
)

func TestArchiedImageAndComposeProvideDesktopCommanderClient(t *testing.T) {
	tests := []struct {
		path    string
		markers []string
	}{
		{
			path: "../../Dockerfile.archied",
			markers: []string{
				"nodejs npm",
			},
		},
		{
			path: "../../config.docker.toml",
			markers: []string{
				`name = "desktop-commander"`,
				`command = "npx"`,
				`"@wonderwhy-er/desktop-commander@0.2.46"`,
				`work_dir = "/workspace"`,
			},
		},
		{
			path: "../../docker-compose.yml",
			markers: []string{
				"./:/workspace",
			},
		},
		{
			path: "../../config.example.toml",
			markers: []string{
				"[[tools.mcp_servers]]",
				`work_dir = "/path/to/workspace"`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, marker := range test.markers {
				if !strings.Contains(string(data), marker) {
					t.Errorf("%s missing %q", test.path, marker)
				}
			}
		})
	}
}
