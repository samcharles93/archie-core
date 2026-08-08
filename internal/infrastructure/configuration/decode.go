package configuration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
)

func decodeConfigFile(path string, target any) error {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		return decodeYAML(path, target)
	case ".toml":
		return decodeTOML(path, target)
	default:
		return fmt.Errorf("%w: config file %s must end in .toml, .yaml, or .yml", ErrUnreadable, path)
	}
}

// decodeTOML decodes a TOML file into target.
func decodeTOML(path string, target any) error {
	if _, err := toml.DecodeFile(path, target); err != nil {
		return fmt.Errorf("%w: parsing %s: %w", ErrUnreadable, path, err)
	}
	return nil
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
