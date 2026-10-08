package configuration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnknownConfFile(t *testing.T) {
	for _, name := range []string{"custom.yaml", "custom.yml", "custom.toml", "custom.txt", "tools.yaml"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("bot_user = \"test\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			conf := filepath.Join(dir, "conf.d")
			if err := os.Mkdir(conf, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(conf, name)
			if err := os.WriteFile(path, []byte("setting: ignored\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := New(nil).Dir(dir, "")
			if name == "tools.yaml" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("config load error = %v, want unknown file %s", err, path)
			}
		})
	}
}
