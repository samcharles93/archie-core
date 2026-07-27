// Package skill parses agentskills.io SKILL.md files and discovers skills
// from the .agents/skills/ directory convention. It follows the same discovery
// pattern as internal/workflow/wfeval (missing directory is not an error).
package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the parsed YAML frontmatter of a SKILL.md file.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
	Metadata    struct {
		Archie *struct {
			Tools    []string `yaml:"tools"`
			Engine   string   `yaml:"engine"`
			Plugins  []string `yaml:"plugins,omitempty"`
			Workflow string   `yaml:"workflow,omitempty"`
		} `yaml:"archie"`
	} `yaml:"metadata"`
}

// CatalogEntry is a lightweight skill reference  --  name + description
// only (~100 tokens). Loaded at daemon startup for skill discovery.
// The full body is loaded on activation via LoadBody. agentskills.io
// progressive disclosure Tier 1.
type CatalogEntry struct {
	Name        string
	Description string
	Workflow    string // from metadata.archie.workflow  --  which workflow this skill handles
	Dir         string // skill directory name
}

// Catalog scans dir/.agents/skills/*/SKILL.md and returns catalog entries
// (Tier 1: name + description + workflow only). Only the frontmatter is
// parsed  --  full bodies are not loaded. Missing directory or a file at the
// skills path returns nil (no error)  --  the caller treats this as "no skills."
func Catalog(dir string) ([]CatalogEntry, error) {
	skillsPath := filepath.Join(dir, skillsDir)
	entries, err := os.ReadDir(skillsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		// A file at .agents/skills (ENOTDIR) is not fatal  --  treat as no skills.
		// Same for permission errors and other non-fatal ReadDir failures.
		return nil, nil //nolint:nilerr // documented above: a non-directory or unreadable skills path means no skills, not a fatal error
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var out []CatalogEntry
	for _, name := range names {
		skillPath := filepath.Join(skillsPath, name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			continue
		}
		fm, _, err := Parse(data)
		if err != nil || fm == nil {
			continue
		}
		wf := ""
		if fm.Metadata.Archie != nil {
			wf = fm.Metadata.Archie.Workflow
		}
		out = append(out, CatalogEntry{
			Name:        fm.Name,
			Description: fm.Description,
			Workflow:    wf,
			Dir:         name,
		})
	}
	return out, nil
}

// SkillForWorkflow returns the catalog entry whose Workflow field matches
// the given workflow name, or nil if no skill declares that workflow.
// Replaces hardcoded workflow→skill lookup tables.
func SkillForWorkflow(catalog []CatalogEntry, workflow string) *CatalogEntry {
	for i := range catalog {
		if catalog[i].Workflow == workflow {
			return &catalog[i]
		}
	}
	return nil
}

// Skill is a parsed SKILL.md file with its frontmatter, body, and any
// bundled Yaegi plugins found in the skill's plugins/ directory.
type Skill struct {
	Frontmatter Frontmatter
	Body        string
	Dir         string   // skill directory name (e.g. "tdd-bugfix")
	Plugins     []Plugin // bundled Yaegi plugins from plugins/*.go
}

const skillsDir = ".agents/skills"

// Parse extracts YAML frontmatter and body from SKILL.md content.
// The frontmatter is delimited by "---" lines. If no frontmatter is found,
// the entire content is returned as the body with a zero Frontmatter.
func Parse(src []byte) (*Frontmatter, string, error) {
	text := string(src)
	if !strings.HasPrefix(text, "---\n") {
		return &Frontmatter{}, text, nil
	}

	// Find the closing "---".
	end := strings.Index(text[4:], "\n---\n")
	if end == -1 {
		// Malformed: starts with --- but no closing delimiter.
		return &Frontmatter{}, text, nil
	}
	end += 4 // adjust for the initial 4-byte skip

	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(text[4:end]), &fm); err != nil {
		return nil, "", fmt.Errorf("yaml frontmatter: %w", err)
	}

	body := strings.TrimSpace(text[end+5:]) // skip "\n---\n"
	return &fm, body, nil
}

// Discover scans dir/.agents/skills/*/SKILL.md and returns a map of skill
// directory name to parsed Skill. Missing directory returns an empty map.
func Discover(dir string) (map[string]Skill, error) {
	skillsPath := filepath.Join(dir, skillsDir)
	entries, err := os.ReadDir(skillsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills dir %s: %w", skillsPath, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	out := make(map[string]Skill, len(names))
	for _, name := range names {
		skillPath := filepath.Join(skillsPath, name, "SKILL.md")
		data, err := os.ReadFile(skillPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // directory exists but no SKILL.md
			}
			return nil, fmt.Errorf("read %s: %w", skillPath, err)
		}
		fm, body, err := Parse(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", skillPath, err)
		}
		var pluginNames []string
		if fm.Metadata.Archie != nil {
			pluginNames = fm.Metadata.Archie.Plugins
		}
		plugins, err := LoadPlugins(dir, name, pluginNames)
		if err != nil {
			return nil, fmt.Errorf("plugins %s: %w", name, err)
		}
		out[name] = Skill{Frontmatter: *fm, Body: body, Dir: name, Plugins: plugins}
	}
	return out, nil
}

// LoadBody loads the raw SKILL.md body for a specific skill directory name.
// Returns "" if the skill directory or SKILL.md does not exist.
func LoadBody(dir, skillDirName string) string {
	skillPath := filepath.Join(dir, skillsDir, skillDirName, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return ""
	}
	_, body, err := Parse(data)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(body)
}
