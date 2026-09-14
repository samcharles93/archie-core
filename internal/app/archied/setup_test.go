package archied

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/setup"
)

func TestSetupSubcommandIsRecognised(t *testing.T) {
	if !IsSetupArgs([]string{setupCommand, "--defaults"}) {
		t.Errorf("IsSetupArgs() did not recognise %q", setupCommand)
	}
	if IsSetupArgs([]string{"-once"}) {
		t.Error("IsSetupArgs() claimed a normal daemon flag")
	}
	if IsSetupArgs(nil) {
		t.Error("IsSetupArgs() claimed an empty argument list")
	}
}

func TestRunSetup_DefaultsWritesLoadableConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()

	var stdout, stderr strings.Builder
	code := RunSetup([]string{"--defaults", "-config", cfgPath}, stdin, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunSetup exit = %d, stderr:\n%s", code, stderr.String())
	}

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}

	doc, err := configuration.New(nil).File(cfgPath)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if doc.Config.BotUser != "archie-bot" {
		t.Errorf("bot_user = %q, want archie-bot", doc.Config.BotUser)
	}

	// The defaults baseline is keyless (ollama), so nothing may be written
	// to the env file.
	if _, err := os.Stat(filepath.Join(dir, "env")); !os.IsNotExist(err) {
		t.Errorf("env file should not exist after a defaults run, got %v", err)
	}
}

// TestRunSetupRejectsSecretValueFlags pins that no secret value can be passed
// on the command line. Secrets are set in a secret engine; this flow only ever
// writes a reference to one. A flag that carried a value would be a second,
// silent way to set a secret -- and would put it in shell history and in every
// process listing, which is why install.sh prompts with read_secret instead.
func TestRunSetupRejectsSecretValueFlags(t *testing.T) {
	for _, flagName := range []string{"-forge-token", "-provider-api-key", "-telegram-token"} {
		t.Run(flagName, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = devNull.Close() }()

			var stdout, stderr strings.Builder
			code := RunSetup([]string{"--defaults", "-config", cfgPath, flagName, "leaked-value"}, devNull, &stdout, &stderr)
			if code != 2 {
				t.Fatalf("RunSetup with %s = exit %d, want 2 (unknown flag); stderr: %s", flagName, code, stderr.String())
			}
			if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
				t.Errorf("config written despite a rejected flag: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "env")); !os.IsNotExist(err) {
				t.Errorf("env file written despite a rejected flag: %v", err)
			}
		})
	}
}

func TestRunSetup_GuidedRequiresTTY(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	stdin, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()

	var stdout, stderr strings.Builder
	code := RunSetup([]string{"-config", cfgPath}, stdin, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("RunSetup exit = 0, want non-zero for a guided run without a TTY")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("config should not be written when a guided non-TTY run is refused, got %v", err)
	}
}

func TestInstallValidatedConfig_ValidationFailureWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	sink := setup.NewEnvFileSink(filepath.Join(dir, "env"))
	if err := sink.Put("env", "ARCHIE_GITHUB_TOKEN", "secret"); err != nil {
		t.Fatal(err)
	}

	err := installValidatedConfig(configuration.New(nil), cfgPath, sink, []byte("this is not toml"))
	if err == nil {
		t.Fatal("installValidatedConfig = nil error, want a load failure")
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("config written despite validation failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "env")); !os.IsNotExist(err) {
		t.Errorf("secret written despite validation failure: %v", err)
	}
}

// TestInstallValidatedConfig_ConfigWriteFailureLeavesNoSecret pins the write
// order the setup package's SecretSink contract depends on: the config is
// installed before any secret is committed, so a secret is never left on disk
// pointing at a config that was never written.
//
// This is not covered by the validation-failure test: inverting the two writes
// leaves every other test in the package green, so this assertion is what
// actually holds the invariant.
func TestInstallValidatedConfig_ConfigWriteFailureLeavesNoSecret(t *testing.T) {
	dir := t.TempDir()
	// The parent directory does not exist, so validation succeeds and the
	// config write fails.
	cfgPath := filepath.Join(dir, "missing", "config.toml")
	envPath := filepath.Join(dir, "env")
	sink := setup.NewEnvFileSink(envPath)
	if err := sink.Put("env", "ARCHIE_GITHUB_TOKEN", "secret"); err != nil {
		t.Fatal(err)
	}

	rendered := []byte("bot_user = \"widget\"\nwork_dir = \"/tmp/archie-setup-work\"\n")
	err := installValidatedConfig(configuration.New(nil), cfgPath, sink, rendered)
	if err == nil {
		t.Fatal("installValidatedConfig = nil error, want a write failure")
	}
	if strings.Contains(err.Error(), "does not load") {
		t.Fatalf("fixture failed validation rather than the write, so this test never exercised the write path: %v", err)
	}
	if _, statErr := os.Stat(envPath); !os.IsNotExist(statErr) {
		t.Errorf("secret committed even though the config was never written: %v", statErr)
	}
}

// TestRunSetup_SecretRefFlagsWriteReferencesWithoutEnvFile pins the reference
// form end to end: naming where a secret is stored writes the reference, and
// setup stores nothing, so no env file appears. The engine here (bws) is one
// setup has no writer for -- which is the point, the value is not setup's.
func TestRunSetup_SecretRefFlagsWriteReferencesWithoutEnvFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devNull.Close() }()

	var stdout, stderr strings.Builder
	code := RunSetup([]string{
		"--defaults", "-config", cfgPath,
		"-provider", "openai", "-model", "gpt-5.4",
		"-provider-secret-ref", "bws:OPENAI_API_KEY",
		"-forge-secret-ref", "bws:ARCHIE_GITHUB_TOKEN",
	}, devNull, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("RunSetup exit = %d, stderr: %s", code, stderr.String())
	}

	doc, err := configuration.New(nil).File(cfgPath)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if got, want := doc.Config.Providers["openai"].APIKey, (config.SecretRef{Engine: "bws", Key: "OPENAI_API_KEY"}); got != want {
		t.Errorf("providers.openai.api_key = %+v, want %+v", got, want)
	}
	if got, want := doc.Config.Forge.Token, (config.SecretRef{Engine: "bws", Key: "ARCHIE_GITHUB_TOKEN"}); got != want {
		t.Errorf("forge.token = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "env")); !os.IsNotExist(err) {
		t.Errorf("env file written: a reference means setup stores no value (stat err: %v)", err)
	}
}

// TestRunSetup_RejectsReferencesNothingWouldUse pins the no-silent-no-op rule.
// A reference for a keyless provider, or for a disabled forge, would sit in the
// config looking configured while nothing read it -- the failure shape this
// whole flow exists to remove -- so it is refused instead.
func TestRunSetup_RejectsReferencesNothingWouldUse(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "provider ref with the keyless self-hosted provider", args: []string{"-provider-secret-ref", "bws:OPENAI_API_KEY"}},
		{name: "forge ref with the forge disabled", args: []string{"-forge-type", "none", "-forge-secret-ref", "bws:ARCHIE_GITHUB_TOKEN"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.toml")
			devNull, err := os.Open(os.DevNull)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = devNull.Close() }()

			args := append([]string{"--defaults", "-config", cfgPath}, tc.args...)
			var stdout, stderr strings.Builder
			if code := RunSetup(args, devNull, &stdout, &stderr); code == 0 {
				t.Fatalf("RunSetup = exit 0, want a refusal; stderr: %s", stderr.String())
			}
			if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
				t.Errorf("config written despite the refusal: %v", err)
			}
		})
	}
}
