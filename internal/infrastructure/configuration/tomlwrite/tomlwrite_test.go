package tomlwrite_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	configtemplate "github.com/samcharles93/archie-core"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/tomlwrite"
)

const sample = `# archied configuration.
poll_interval = "60s"
label = "archie" # display label

[forge]
type = "github"
host = "https://github.com"
token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }

# [chat]
# operator = "Ada Lovelace"
# workspace = "~/.local/share/archie/workspace"
# max_steps = 100

[budgets]
max_steps = 60
`

func TestApply_ActiveKey_ReplacesValuePreservesComment(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "", Key: "label", Value: tomlwrite.String("acme")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample, `label = "archie" # display label`, `label = "acme" # display label`, 1)
	if string(out) != want {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestApply_ActiveInlineTable_Replaces(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "forge", Key: "token", Value: tomlwrite.Ref("env", "GITEA_TOKEN")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(sample,
		`token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }`,
		`token = { engine = "env", key = "GITEA_TOKEN" }`, 1)
	if string(out) != want {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestApply_CommentedTable_UncommentsOnlyRequestedKey(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "chat", Key: "operator", Value: tomlwrite.String("Ada Lovelace")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "\n[chat]\noperator = \"Ada Lovelace\"\n# workspace = \"~/.local/share/archie/workspace\"\n# max_steps = 100\n") {
		t.Fatalf("expected header and operator uncommented, siblings left commented, got:\n%s", got)
	}
}

func TestApply_MissingKeyInExistingActiveTable_Appends(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "budgets", Key: "max_tokens", Value: "400000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "[budgets]\nmax_steps = 60\nmax_tokens = 400000\n") {
		t.Fatalf("expected max_tokens appended after max_steps, got:\n%s", got)
	}
}

func TestApply_NewTable_AppendedAtEnd(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "providers.anthropic", Key: "class", Value: tomlwrite.String("anthropic")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.HasSuffix(got, "\n\n[providers.anthropic]\nclass = \"anthropic\"\n") {
		t.Fatalf("expected new table appended at end, got:\n%s", got)
	}
}

// TestApply_UnrelatedLinesByteIdentical is the archie-core-rs9 acceptance
// criterion: changing one value must not touch any other line, comments
// included.
func TestApply_UnrelatedLinesByteIdentical(t *testing.T) {
	out, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "", Key: "label", Value: tomlwrite.String("acme")},
	})
	if err != nil {
		t.Fatal(err)
	}
	beforeLines := strings.Split(sample, "\n")
	afterLines := strings.Split(string(out), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("line count changed: %d -> %d", len(beforeLines), len(afterLines))
	}
	for i := range beforeLines {
		if strings.HasPrefix(beforeLines[i], "label") {
			continue
		}
		if beforeLines[i] != afterLines[i] {
			t.Fatalf("line %d changed unexpectedly:\n- %q\n+ %q", i, beforeLines[i], afterLines[i])
		}
	}
}

// TestApply_Idempotent_SecondRunLeavesOtherEditByteIdentical mirrors a
// real setup re-run: apply one edit, then apply a second, different edit
// to the result, and check the first edit's line -- and everything else
// unrelated to either edit -- is untouched.
func TestApply_Idempotent_SecondRunLeavesOtherEditByteIdentical(t *testing.T) {
	first, err := tomlwrite.Apply([]byte(sample), []tomlwrite.Edit{
		{Table: "", Key: "label", Value: tomlwrite.String("acme")},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tomlwrite.Apply(first, []tomlwrite.Edit{
		{Table: "forge", Key: "host", Value: tomlwrite.String("https://gitea.example.com")},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstLines := strings.Split(string(first), "\n")
	secondLines := strings.Split(string(second), "\n")
	if len(firstLines) != len(secondLines) {
		t.Fatalf("line count changed: %d -> %d", len(firstLines), len(secondLines))
	}
	for i := range firstLines {
		if strings.HasPrefix(secondLines[i], "host") {
			continue
		}
		if firstLines[i] != secondLines[i] {
			t.Fatalf("line %d changed unexpectedly on second run:\n- %q\n+ %q", i, firstLines[i], secondLines[i])
		}
	}
	if !strings.Contains(string(second), `label = "acme"`) {
		t.Fatalf("first run's edit was lost on second run:\n%s", second)
	}
}

func TestGenerate_RealTemplate_ProducesReadableConfig(t *testing.T) {
	out, err := tomlwrite.Generate(configtemplate.Example, []tomlwrite.Edit{
		{Table: "", Key: "bot_user", Value: tomlwrite.String("acme-archie")},
		{Table: "forge", Key: "type", Value: tomlwrite.String("gitea")},
		{Table: "forge", Key: "host", Value: tomlwrite.String("https://gitea.example.com")},
		{Table: "forge", Key: "token", Value: tomlwrite.Ref("env", "ARCHIE_GITEA_TOKEN")},
		{Table: "chat", Key: "operator", Value: tomlwrite.String("Ada Lovelace")},
		{Table: "providers.anthropic", Key: "class", Value: tomlwrite.String("anthropic")},
		{Table: "providers.anthropic", Key: "api_key_env", Value: tomlwrite.String("ANTHROPIC_API_KEY")},
	})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	got := string(out)
	for _, want := range []string{
		`bot_user = "acme-archie"`,
		`type = "gitea"`,
		`host = "https://gitea.example.com"`,
		`token = { engine = "env", key = "ARCHIE_GITEA_TOKEN" }`,
		"[chat]\noperator = \"Ada Lovelace\"",
		"[providers.anthropic]\napi_key_env = \"ANTHROPIC_API_KEY\"\nclass = \"anthropic\"",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected generated config to contain %q, got:\n%s", want, got)
		}
	}
	// The documentation comment above [providers.openai] must survive --
	// this is the whole point of not re-marshalling.
	if !strings.Contains(got, "# Optional interactive-chat catalog.") {
		t.Fatalf("expected surrounding documentation to survive, got:\n%s", got)
	}

	// The result must still be well-formed TOML that decodes into the
	// runtime config model -- Apply edits text, but the output still has
	// to parse.
	var decoded config.Config
	if _, err := toml.Decode(got, &decoded); err != nil {
		t.Fatalf("generated config does not parse as TOML: %v\n%s", err, got)
	}
	if decoded.BotUser != "acme-archie" {
		t.Fatalf("bot_user did not round-trip: got %q", decoded.BotUser)
	}
	if decoded.Forge.Token.Key != "ARCHIE_GITEA_TOKEN" {
		t.Fatalf("forge.token did not round-trip: got %+v", decoded.Forge.Token)
	}
}
