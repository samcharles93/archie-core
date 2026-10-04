package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/skill"
)

// SkillsKind is the control-plane resource holding the org's skills: SKILL.md
// documents under their skill name.
const SkillsKind = "skills"

// SkillCollection is the skills document.
type SkillCollection struct {
	Skills []SkillEntry `json:"skills"`
}

// SkillEntry is one skill. Content is its SKILL.md.
type SkillEntry struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func skillsDefinition() Definition {
	return Definition{
		Kind:      SkillsKind,
		Title:     "Skills",
		Document:  SkillCollection{},
		ApplyMode: "live",
		Seed:      func(config.Config) any { return SkillCollection{Skills: []SkillEntry{}} },
		Validate: func(input []byte) error {
			return validateAs(input, validateSkills)
		},
	}
}

func validateSkills(c SkillCollection) error {
	seen := map[string]bool{}
	for _, entry := range c.Skills {
		if entry.Name == "" || seen[entry.Name] {
			return fmt.Errorf("%w: skill name %q is empty or repeated", ErrValidation, entry.Name)
		}
		seen[entry.Name] = true
		fm, _, err := skill.Parse([]byte(entry.Content))
		if err != nil {
			return fmt.Errorf("%w: skill %q: %w", ErrValidation, entry.Name, err)
		}
		if fm.Name != entry.Name {
			return fmt.Errorf("%w: skill %q: SKILL.md names it %q", ErrValidation, entry.Name, fm.Name)
		}
	}
	return nil
}

// NewSkillPackageProjector builds the projector for storepkg.FamilySkills.
func NewSkillPackageProjector(resources ResourceStore, ledger storepkg.ProjectionLedger) (storepkg.FamilyProjector, error) {
	return newListProjector(resources, ledger, listFamily[SkillEntry]{
		kind:   SkillsKind,
		family: storepkg.FamilySkills,
		decode: func(value []byte) ([]SkillEntry, error) {
			c, err := decodeSkills(value)
			return c.Skills, err
		},
		encode: func(entries []SkillEntry) ([]byte, error) {
			return encodeSkills(SkillCollection{Skills: entries})
		},
		id: func(e SkillEntry) string { return e.Name },
		parse: func(_ string, content []byte) (SkillEntry, error) {
			fm, _, err := skill.Parse(content)
			if err != nil {
				return SkillEntry{}, err
			}
			if fm.Name == "" {
				return SkillEntry{}, errors.New("no frontmatter name")
			}
			return SkillEntry{Name: fm.Name, Content: string(content)}, nil
		},
	})
}

func decodeSkills(value []byte) (SkillCollection, error) {
	collection := SkillCollection{Skills: []SkillEntry{}}
	if value == nil {
		return collection, nil
	}
	if err := json.Unmarshal(value, &collection); err != nil {
		return SkillCollection{}, fmt.Errorf("decode %s: %w", SkillsKind, err)
	}
	return collection, nil
}

func encodeSkills(collection SkillCollection) ([]byte, error) {
	if err := validateSkills(collection); err != nil {
		return nil, err
	}
	return json.Marshal(collection)
}
