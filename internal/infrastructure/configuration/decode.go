package configuration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
)

// decodeConfigFileKeys decodes path into target, dispatching on extension,
// additionally returning target's undecoded top-level keys for a TOML
// source (see decodeTOMLKeys). A YAML source has no equivalent hook yet
// (plan-config-drift.md step 1 note) and always reports none -- unchanged
// behaviour, not a claim of completeness.
func decodeConfigFileKeys(path string, target any) ([]string, error) {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		return nil, decodeYAML(path, target)
	case ".toml":
		return decodeTOMLKeys(path, target)
	default:
		return nil, fmt.Errorf("%w: config file %s must end in .toml, .yaml, or .yml", ErrUnreadable, path)
	}
}

// decodeFileMapping parses a configuration file into the nested mapping it
// carries, with no typed target. An overlay needs this shape: a typed decode
// cannot distinguish a key the file omits from a key it sets to the zero
// value, and that distinction is what carrying the fields of a partially
// addressed map entry forward is built on (see applyFileOverlay).
func decodeFileMapping(path string) (map[string]any, error) {
	var doc map[string]any
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: reading %s: %w", ErrUnreadable, path, err)
		}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%w: parsing %s: %w", ErrUnreadable, path, err)
		}
	case ".toml":
		if _, err := toml.DecodeFile(path, &doc); err != nil {
			return nil, fmt.Errorf("%w: parsing %s: %w", ErrUnreadable, path, err)
		}
	default:
		return nil, fmt.Errorf("%w: config file %s must end in .toml, .yaml, or .yml", ErrUnreadable, path)
	}
	return doc, nil
}

// decodeTOMLKeys decodes a TOML file into target, additionally
// returning the top-level dotted key paths present in the file that
// target did not consume (toml.MetaData.Undecoded()). A single decode
// target's undecoded set is not by itself "unknown" -- see Hazard 1 in
// .local/issue-tracker/plan-config-drift.md, one file can legitimately
// feed more than one target -- callers combine this with the other
// target's undecoded set before treating anything as a real unknown key.
func decodeTOMLKeys(path string, target any) ([]string, error) {
	meta, err := toml.DecodeFile(path, target)
	if err != nil {
		return nil, fmt.Errorf("%w: parsing %s: %w", ErrUnreadable, path, err)
	}
	undecoded := meta.Undecoded()
	if len(undecoded) == 0 {
		return nil, nil
	}
	keys := make([]string, len(undecoded))
	for i, k := range undecoded {
		keys[i] = k.String()
	}
	return keys, nil
}

// decodeYAML decodes a YAML file into target.
//
// Every decode error names its file. The previous loadMainConfig returned
// bare yaml.Unmarshal and toml.DecodeFile errors, so a syntax error in one
// of several files gave no clue which one to open.
func decodeYAML(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrUnreadable, path, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: parsing %s: %w", ErrUnreadable, path, err)
	}
	return nil
}

// decodeFeature decodes a feature file into the part of cfg it describes.
//
// Most feature files carry top-level keys matching Config fields and decode
// into cfg directly. Memory and tools are sub-structs whose keys sit at the
// top level of their own file rather than nested under a "memory:" or
// "tools:" key, so they decode into the sub-struct.
func decodeFeature(cfg *config.Config, feature Feature, path string) error {
	switch feature {
	case FeatureMemory:
		return decodeYAML(path, &cfg.Memory)
	case FeatureTools:
		return decodeYAML(path, &cfg.Tools)
	default:
		return decodeYAML(path, cfg)
	}
}

// decodeExtra decodes an unrecognised conf.d/ file into cfg.Extra under name.
//
// Unlike the previous implementation this reports failures instead of
// skipping them: silently dropping a file the operator wrote, because it
// happened to be unreadable or malformed, presents as the setting simply not
// taking effect.
func decodeExtra(cfg *config.Config, name, path string) error {
	var value any
	if err := decodeYAML(path, &value); err != nil {
		return err
	}
	if cfg.Extra == nil {
		cfg.Extra = make(map[string]any)
	}
	cfg.Extra[name] = value
	return nil
}
