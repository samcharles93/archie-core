package configuration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestArtifactsConfigDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[forge]\ntype = \"gitea\"\nhost = \"https://git.example.test\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Artifacts.BaseURL != "" {
		t.Errorf("BaseURL = %q, want empty so the sender stays disabled", cfg.Artifacts.BaseURL)
	}
	if want := (config.SecretRef{Engine: "env", Key: "WORKSPACE_INGEST_TOKEN"}); cfg.Artifacts.Token != want {
		t.Errorf("Token = %+v, want the documented fallback %+v", cfg.Artifacts.Token, want)
	}
}

func TestArtifactsConfigPreservesExplicitValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := "bot_user = \"widget\"\n" +
		"[artifacts]\nbase_url = \"https://offloaded.dev\"\n" +
		"token = { engine = \"env\", key = \"ARCHIE_GITHUB_TOKEN\" }\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Artifacts.BaseURL != "https://offloaded.dev" {
		t.Errorf("BaseURL = %q, want the explicit value", cfg.Artifacts.BaseURL)
	}
	if want := (config.SecretRef{Engine: "env", Key: "ARCHIE_GITHUB_TOKEN"}); cfg.Artifacts.Token != want {
		t.Errorf("Token = %+v, want %+v", cfg.Artifacts.Token, want)
	}
}
