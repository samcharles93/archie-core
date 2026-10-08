package configuration

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
)

// decodeConfigFileKeys decodes path into target by extension and returns
// undecoded top-level keys for TOML files.
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

// decodeFileMapping parses a configuration file into an untyped nested map.
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

// decodeTOMLKeys decodes a TOML file into target and returns the top-level
// keys target did not consume.
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

// decodeYAML decodes a YAML file into target. Errors name the file.
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
// Memory and tools files decode into their sub-structs. Overlay feature files
// fold over the base.
func decodeFeature(cfg *config.Config, feature Feature, path string, layer Layer) error {
	target := any(cfg)
	switch feature {
	case FeatureMemory:
		target = &cfg.Memory
	case FeatureTools:
		target = &cfg.Tools
	}
	if layer == LayerOverlay {
		_, err := applyOverlayFile(path, target)
		return err
	}
	return decodeYAML(path, target)
}
