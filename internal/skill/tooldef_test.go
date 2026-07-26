package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ── progressive disclosure: catalog → tool bridge ────────────────────
//
// A chat turn's tool set should offer ONE tool (not one per skill) that
// lets the model activate any cataloged skill by name  --  mirroring the
// two-tier disclosure Catalog/LoadBody already implement: the tool's
// schema enumerates every catalog entry's name+description (Tier 1,
// cheap), and calling it returns the full SKILL.md body (Tier 2, loaded
// only on activation).

func writeSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, ".agents", "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestActivateToolNameAndSchema(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "does alpha things", "alpha body")
	writeSkill(t, dir, "beta", "does beta things", "beta body")

	catalog, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := ActivateTool(dir, catalog)

	if entry.Name != "skill_activate" {
		t.Errorf("Name = %q, want skill_activate", entry.Name)
	}
	if entry.Toolset != "skill" {
		t.Errorf("Toolset = %q, want skill", entry.Toolset)
	}
	if entry.Handler == nil {
		t.Fatal("Handler is nil")
	}

	enumRaw, ok := enumFromSchema(entry.Schema)
	if !ok {
		t.Fatal("schema does not expose a name enum")
	}
	if len(enumRaw) != 2 {
		t.Fatalf("schema enum has %d entries, want 2", len(enumRaw))
	}
}

func TestActivateToolHandlerReturnsFullBody(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "does alpha things", "This is the full alpha body.")

	catalog, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := ActivateTool(dir, catalog)

	out, err := entry.Handler(context.Background(), map[string]any{"name": "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	body, ok := out.(string)
	if !ok {
		t.Fatalf("output type = %T, want string", out)
	}
	if body != "This is the full alpha body." {
		t.Errorf("body = %q", body)
	}
}

func TestActivateToolHandlerRejectsUnknownSkill(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "does alpha things", "alpha body")
	catalog, _ := Catalog(dir)
	entry := ActivateTool(dir, catalog)

	_, err := entry.Handler(context.Background(), map[string]any{"name": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown skill name")
	}
}

func TestActivateToolHandlerRejectsMissingName(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "alpha", "does alpha things", "alpha body")
	catalog, _ := Catalog(dir)
	entry := ActivateTool(dir, catalog)

	_, err := entry.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing name argument")
	}
}

func TestActivateToolWithEmptyCatalogReturnsNilEntry(t *testing.T) {
	// No skills configured  --  don't clutter the tool set with a dead tool.
	dir := t.TempDir()
	entry := ActivateTool(dir, nil)
	if entry != nil {
		t.Fatalf("expected nil entry for empty catalog, got %+v", entry)
	}
}

func TestActivateToolHandlerUsesSkillDirNotDisplayName(t *testing.T) {
	// CatalogEntry.Name comes from frontmatter and CatalogEntry.Dir is the
	// directory name  --  they can differ. LoadBody keys off the directory
	// name, so the tool's activation argument must match what the catalog
	// exposes as selectable (Name), and the handler must resolve it back
	// to the correct Dir internally.
	dir := t.TempDir()
	skillDir := filepath.Join(dir, ".agents", "skills", "dir-name-differs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: Display Name\ndescription: d\n---\nfull body here\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	catalog, err := Catalog(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := ActivateTool(dir, catalog)

	out, err := entry.Handler(context.Background(), map[string]any{"name": "Display Name"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "full body here" {
		t.Errorf("body = %v", out)
	}
}

// enumFromSchema digs the "name" property's enum out of a JSON-Schema-shaped
// map, tolerating the concrete map/slice types produced by our own
// ActivateTool (no external JSON round-trip in this test).
func enumFromSchema(schema map[string]any) ([]any, bool) {
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	nameProp, ok := props["name"].(map[string]any)
	if !ok {
		return nil, false
	}
	enum, ok := nameProp["enum"].([]any)
	return enum, ok
}
