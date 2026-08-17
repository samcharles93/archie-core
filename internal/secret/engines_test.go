package secret_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/traefik/yaegi/stdlib/unrestricted"

	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/secret/enginehost"
)

// engineSymbols mirrors the daemon's secret-engine wiring in
// cmd/archied/provider_secrets.go: the generated secret symbols, the
// host-side enginehost helper (plugins run CLIs through it because
// yaegi's interpreted os/exec cannot pass env to children), and
// stdlib.Unrestricted for parity with the production wiring.
var engineSymbols = []map[string]map[string]reflect.Value{symbols, enginehost.Symbols, unrestricted.Symbols}

// ── shipped engine plugin tests ─────────────────────────────────────
//
// These tests exercise the real plugin files shipped in
// examples/secret-engines/, not inline fixtures: the artifact an operator
// copies into secret_engine_dir is exactly what is tested here. Each
// engine shells out to a CLI, so a fake executable is placed on PATH and
// the engine's env vars are set, mirroring TestBWSEngineLoadsNamedSecretsOnce.

// enginePluginDir returns the shipped plugin examples dir relative to this
// package (go test runs with cwd = the package dir).
func enginePluginDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "secret-engines"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// loadShippedEngines loads every engine plugin from the examples dir into a
// fresh registry and asserts the sops, vault and age engines registered.
func loadShippedEngines(t *testing.T) *secret.Registry {
	t.Helper()
	r := secret.NewRegistry()
	n, err := r.LoadDir(enginePluginDir(t), engineSymbols...)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if n < 3 {
		t.Fatalf("LoadDir loaded %d engines, want at least 3 (sops, vault, age)", n)
	}
	for _, name := range []string{"sops", "vault", "age"} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("engine %q not registered after LoadDir", name)
		}
	}
	return r
}

// fakeCLI writes an executable script named name into a fresh temp dir and
// points PATH at it.
func fakeCLI(t *testing.T, name, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestEachEngineLoadsStandalone proves each shipped plugin file loads and
// registers when copied into an otherwise-empty plugin dir — the exact
// operator install path.
func TestEachEngineLoadsStandalone(t *testing.T) {
	for _, name := range []string{"sops", "vault", "age"} {
		t.Run(name, func(t *testing.T) {
			src := filepath.Join(enginePluginDir(t), name+".go")
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("read %s: %v", src, err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name+".go"), data, 0o644); err != nil {
				t.Fatal(err)
			}
			r := secret.NewRegistry()
			n, err := r.LoadDir(dir, engineSymbols...)
			if err != nil {
				t.Fatal(err)
			}
			if n != 1 {
				t.Fatalf("LoadDir loaded %d engines, want 1", n)
			}
			if _, ok := r.Get(name); !ok {
				t.Fatalf("engine %q not registered", name)
			}
		})
	}
}

// ── sops ────────────────────────────────────────────────────────────

func TestSopsEngineResolvesViaCLI(t *testing.T) {
	fakeCLI(t, "sops", `#!/bin/sh
printf '%s' 'super-secret-value'
`)
	t.Setenv("SOPS_FILE", "/etc/archie/secrets.yaml")
	r := loadShippedEngines(t)
	e, _ := r.Get("sops")

	got, err := e.Resolve("DB_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "super-secret-value" {
		t.Fatalf("Resolve() = %q, want super-secret-value", got)
	}
}

func TestSopsEngineRequiresSOPSFile(t *testing.T) {
	fakeCLI(t, "sops", `#!/bin/sh
printf '%s' 'x'
`)
	r := loadShippedEngines(t)
	e, _ := r.Get("sops")

	if _, err := e.Resolve("DB_PASSWORD"); err == nil {
		t.Fatal("Resolve() succeeded without SOPS_FILE, want error")
	}
}

func TestSopsEngineRejectsInvalidKey(t *testing.T) {
	fakeCLI(t, "sops", `#!/bin/sh
printf '%s' 'x'
`)
	t.Setenv("SOPS_FILE", "/etc/archie/secrets.yaml")
	r := loadShippedEngines(t)
	e, _ := r.Get("sops")

	if _, err := e.Resolve(`bad"key`); err == nil {
		t.Fatal("Resolve() accepted a key containing a quote, want error")
	}
}

func TestSopsEngineCLIFailure(t *testing.T) {
	fakeCLI(t, "sops", `#!/bin/sh
echo 'sops: cannot open file' >&2
exit 1
`)
	t.Setenv("SOPS_FILE", "/etc/archie/secrets.yaml")
	r := loadShippedEngines(t)
	e, _ := r.Get("sops")

	if _, err := e.Resolve("DB_PASSWORD"); err == nil {
		t.Fatal("Resolve() succeeded with a failing CLI, want error")
	}
}

func TestSopsEngineMissingKeyErrorsOnEmptyOutput(t *testing.T) {
	fakeCLI(t, "sops", `#!/bin/sh
printf '%s' ''
`)
	t.Setenv("SOPS_FILE", "/etc/archie/secrets.yaml")
	r := loadShippedEngines(t)
	e, _ := r.Get("sops")

	if _, err := e.Resolve("NOPE"); err == nil {
		t.Fatal("Resolve() succeeded with empty CLI output, want error")
	}
}

// ── vault ───────────────────────────────────────────────────────────

// vaultEchoCLI is a fake vault CLI that prints "<path>:<field>" so tests
// can assert exactly which KV path and field the engine targeted.
const vaultEchoCLI = `#!/bin/sh
field=""
path=""
for arg in "$@"; do
	case "$arg" in
		-field=*) field="${arg#-field=}" ;;
		*) path="$arg" ;;
	esac
done
printf '%s:%s' "$path" "$field"
`

func TestVaultEngineResolvesFullPathKey(t *testing.T) {
	fakeCLI(t, "vault", vaultEchoCLI)
	t.Setenv("VAULT_ADDR", "https://vault.example:8200")
	t.Setenv("VAULT_TOKEN", "test-token")
	r := loadShippedEngines(t)
	e, _ := r.Get("vault")

	got, err := e.Resolve("secret/archie/DB_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret/archie:DB_PASSWORD" {
		t.Fatalf("Resolve() targeted %q, want secret/archie:DB_PASSWORD", got)
	}
}

func TestVaultEngineBareKeyUsesSecretPath(t *testing.T) {
	fakeCLI(t, "vault", vaultEchoCLI)
	t.Setenv("VAULT_ADDR", "https://vault.example:8200")
	t.Setenv("VAULT_TOKEN", "test-token")
	t.Setenv("VAULT_SECRET_PATH", "secret/archie")
	r := loadShippedEngines(t)
	e, _ := r.Get("vault")

	got, err := e.Resolve("DB_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret/archie:DB_PASSWORD" {
		t.Fatalf("Resolve() targeted %q, want secret/archie:DB_PASSWORD", got)
	}
}

func TestVaultEngineBareKeyWithoutPathFails(t *testing.T) {
	fakeCLI(t, "vault", `#!/bin/sh
printf '%s' 'x'
`)
	t.Setenv("VAULT_ADDR", "https://vault.example:8200")
	t.Setenv("VAULT_TOKEN", "test-token")
	r := loadShippedEngines(t)
	e, _ := r.Get("vault")

	if _, err := e.Resolve("DB_PASSWORD"); err == nil {
		t.Fatal("bare key without VAULT_SECRET_PATH should fail")
	}
}

func TestVaultEngineRequiresAddrAndToken(t *testing.T) {
	fakeCLI(t, "vault", `#!/bin/sh
printf '%s' 'x'
`)
	r := loadShippedEngines(t)
	e, _ := r.Get("vault")

	if _, err := e.Resolve("secret/archie/DB_PASSWORD"); err == nil {
		t.Fatal("Resolve() succeeded without VAULT_ADDR/VAULT_TOKEN, want error")
	}
}

// ── age ─────────────────────────────────────────────────────────────

const agePlaintext = `# archie secrets

DB_PASSWORD=age-secret-value
API_KEY=other=with=equals
EMPTY=
# trailing comment
`

func TestAgeEngineResolvesViaCLI(t *testing.T) {
	fakeCLI(t, "age", "#!/bin/sh\nprintf '%s' '"+agePlaintext+"'\n")
	t.Setenv("AGE_FILE", "/etc/archie/secrets.age")
	t.Setenv("AGE_IDENTITY", "/etc/archie/age.key")
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	got, err := e.Resolve("DB_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "age-secret-value" {
		t.Fatalf("Resolve() = %q, want age-secret-value", got)
	}
}

func TestAgeEngineSkipsCommentsBlanksAndSplitsAtFirstEquals(t *testing.T) {
	fakeCLI(t, "age", "#!/bin/sh\nprintf '%s' '"+agePlaintext+"'\n")
	t.Setenv("AGE_FILE", "/etc/archie/secrets.age")
	t.Setenv("AGE_IDENTITY", "/etc/archie/age.key")
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	got, err := e.Resolve("API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if got != "other=with=equals" {
		t.Fatalf("Resolve() = %q, want other=with=equals (split at first =)", got)
	}
}

func TestAgeEngineMissingKey(t *testing.T) {
	fakeCLI(t, "age", "#!/bin/sh\nprintf '%s' '"+agePlaintext+"'\n")
	t.Setenv("AGE_FILE", "/etc/archie/secrets.age")
	t.Setenv("AGE_IDENTITY", "/etc/archie/age.key")
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	if _, err := e.Resolve("NOPE"); err == nil {
		t.Fatal("Resolve() succeeded for a missing key, want error")
	}
}

func TestAgeEngineEmptyValueIsError(t *testing.T) {
	fakeCLI(t, "age", "#!/bin/sh\nprintf '%s' '"+agePlaintext+"'\n")
	t.Setenv("AGE_FILE", "/etc/archie/secrets.age")
	t.Setenv("AGE_IDENTITY", "/etc/archie/age.key")
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	if _, err := e.Resolve("EMPTY"); err == nil {
		t.Fatal("Resolve() accepted an empty value, want error")
	}
}

func TestAgeEngineRequiresEnv(t *testing.T) {
	fakeCLI(t, "age", `#!/bin/sh
printf '%s' 'x'
`)
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	if _, err := e.Resolve("DB_PASSWORD"); err == nil {
		t.Fatal("Resolve() succeeded without AGE_FILE/AGE_IDENTITY, want error")
	}
}

func TestAgeEngineCLIFailure(t *testing.T) {
	fakeCLI(t, "age", `#!/bin/sh
echo 'age: cannot decrypt' >&2
exit 1
`)
	t.Setenv("AGE_FILE", "/etc/archie/secrets.age")
	t.Setenv("AGE_IDENTITY", "/etc/archie/age.key")
	r := loadShippedEngines(t)
	e, _ := r.Get("age")

	if _, err := e.Resolve("DB_PASSWORD"); err == nil {
		t.Fatal("Resolve() succeeded with a failing CLI, want error")
	}
}
