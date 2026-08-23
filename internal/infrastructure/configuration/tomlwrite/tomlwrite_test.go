package tomlwrite_test

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

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
		{Table: "budgets", Key: "wall_clock", Value: "\"45m\""},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "[budgets]\nmax_steps = 60\nwall_clock = \"45m\"\n") {
		t.Fatalf("expected wall_clock appended after max_steps, got:\n%s", got)
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

// [models], [indexing], [web], and [notify] in the real template all carry
// a trailing "# ..." comment on the header line itself
// ("[models] # role -> ..."). The header regexes were anchored with no
// allowance for that, so Apply's table-tracking never recognised these as
// table headers at all -- an edit targeting "models" fell through every
// matching stage and got appended as a brand-new duplicate [models] table
// at EOF, which BurntSushi/toml then refuses to decode ("Key 'models' has
// already been defined"). archied setup's models step targets exactly this
// table.
func TestApply_ActiveHeaderWithTrailingComment_IsRecognised(t *testing.T) {
	src := `[models] # role -> ai-sdk runtime ref
triage = "openai/gpt-5.4"

[budgets]
max_steps = 60
`
	out, err := tomlwrite.Apply([]byte(src), []tomlwrite.Edit{
		{Table: "models", Key: "planner", Value: tomlwrite.String("openai/gpt-5.4")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "planner") != 1 {
		t.Fatalf("expected exactly one planner line, got:\n%s", got)
	}
	if strings.Contains(got, "\n[models]\n") {
		t.Fatalf("a second, commentless [models] table was appended instead of reusing the commented-header one:\n%s", got)
	}
	var decoded config.Config
	if _, err := toml.Decode(got, &decoded); err != nil {
		t.Fatalf("output does not parse as TOML: %v\n%s", err, got)
	}
	if decoded.Models["triage"] != "openai/gpt-5.4" || decoded.Models["planner"] != "openai/gpt-5.4" {
		t.Fatalf("models did not round-trip: got %+v", decoded.Models)
	}
}
