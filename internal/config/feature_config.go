package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// knownFeatureNames is the set of recognised config.<name>.yaml feature files.
// Any config.<name>.yaml file whose <name> is not in this set is rejected  -- 
// silent ignores would hide typos.
var knownFeatureNames = map[string]bool{
	"gateway":    true,
	"tools":      true,
	"memory":     true,
	"models":     true,
	"identities": true,
}

// LoadDir loads configuration from a directory tree.
//
//	~/.config/archie/config.yaml            --  daemon-level fields
//	~/.config/archie/config.gateway.yaml    --  chat channels, platforms
//	~/.config/archie/config.tools.yaml      --  MCP servers, tool policy
//	~/.config/archie/config.memory.yaml     --  memory provider config
//	~/.config/archie/config.models.yaml     --  LLM providers and models
//	~/.config/archie/config.identities.yaml  --  identity configs
//	~/.config/archie/conf.d/*.yaml           --  additional feature files
//
// Missing feature files mean "feature disabled"  --  no error, just zero values.
// Legacy config.toml is used as a fallback when config.yaml is absent.
// YAML takes precedence over TOML when both exist.
//
// When overlayDir is non-empty, files from overlayDir are decoded on top of
// baseDir  --  only the fields an overlay file declares are overridden; everything
// else is inherited from the base.
func LoadDir(baseDir, overlayDir string) (Config, error) {
	var cfg Config
	if err := loadDirInto(&cfg, baseDir, true); err != nil {
		return Config{}, err
	}
	if overlayDir != "" {
		if err := loadDirInto(&cfg, overlayDir, false); err != nil {
			return Config{}, err
		}
	}
	return finalize(cfg)
}

// loadDirInto scans dir for config files and decodes them into cfg.
// When requireMain is true the directory must contain config.yaml or
// config.toml; when false (overlay mode) a missing main config is
// fine  --  only feature and conf.d/ files are applied.
func loadDirInto(cfg *Config, dir string, requireMain bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("config dir %s: %w", dir, err)
	}

	var mainYAML, mainTOML string
	featurePaths := map[string]string{} // feature name → path
	var unknownConfigFiles []string
	extraPaths := map[string]string{} // name → path (conf.d/ extras)

	for _, e := range entries {
		name := e.Name()

		// Handle conf.d/ subdirectory.
		if e.IsDir() {
			if name != "conf.d" {
				continue
			}
			confEntries, confErr := os.ReadDir(filepath.Join(dir, name))
			if confErr != nil {
				if os.IsNotExist(confErr) {
					continue
				}
				return fmt.Errorf("reading conf.d: %w", confErr)
			}
			for _, ce := range confEntries {
				cn := ce.Name()
				if ce.IsDir() {
					continue
				}
				if !strings.HasSuffix(cn, ".yaml") && !strings.HasSuffix(cn, ".yml") {
					continue
				}
				feature := strings.TrimSuffix(strings.TrimSuffix(cn, ".yaml"), ".yml")
				fullPath := filepath.Join(dir, "conf.d", cn)
				if knownFeatureNames[feature] {
					if _, exists := featurePaths[feature]; exists {
						return fmt.Errorf("duplicate feature %q: both config.%s.yaml and conf.d/%s.yaml exist", feature, feature, feature)
					}
					featurePaths[feature] = fullPath
				} else {
					extraPaths[feature] = fullPath
				}
			}
			continue
		}

		// Skip non-YAML, non-TOML files.
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".toml") {
			continue
		}

		// Main config files.
		if name == "config.yaml" || name == "config.yml" {
			mainYAML = filepath.Join(dir, name)
			continue
		}
		if name == "config.toml" {
			mainTOML = filepath.Join(dir, name)
			continue
		}

		// Feature files: config.<feature>.yaml
		if strings.HasPrefix(name, "config.") && (strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			rest := strings.TrimPrefix(name, "config.")
			feature := strings.TrimSuffix(strings.TrimSuffix(rest, ".yaml"), ".yml")
			if !knownFeatureNames[feature] {
				unknownConfigFiles = append(unknownConfigFiles, name)
				continue
			}
			if _, exists := featurePaths[feature]; exists {
				return fmt.Errorf("duplicate feature %q: both config.%s.yaml and another file define it", feature, feature)
			}
			featurePaths[feature] = filepath.Join(dir, name)
			continue
		}
	}

	// Reject unknown config.<name>.yaml files.
	if len(unknownConfigFiles) > 0 {
		return fmt.Errorf("unrecognized config file %q (known features: gateway, tools, memory, models, identities)", unknownConfigFiles[0])
	}

	// Load main config.
	if mainYAML != "" {
		data, err := os.ReadFile(mainYAML)
		if err != nil {
			return fmt.Errorf("reading %s: %w", mainYAML, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", mainYAML, err)
		}
	} else if mainTOML != "" {
		if _, err := toml.DecodeFile(mainTOML, cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", mainTOML, err)
		}
	} else if requireMain {
		return fmt.Errorf("no config.yaml or config.toml found in %s", dir)
	}

	// Load known feature files.
	for feature, path := range featurePaths {
		if err := loadFeatureFile(cfg, feature, path); err != nil {
			return err
		}
	}

	// Load extra conf.d/ files into Extra map.
	for name, path := range extraPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var val any
		if err := yaml.Unmarshal(data, &val); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		if cfg.Extra == nil {
			cfg.Extra = make(map[string]any)
		}
		cfg.Extra[name] = val
	}

	return nil
}

// loadFeatureFile decodes a feature file's YAML into the correct part of
// Config. Most feature files map to top-level Config fields and can be
// decoded with yaml.Unmarshal(data, cfg) directly. Memory and tools are
// sub-structs whose YAML keys live at the top level of the file, not nested
// under a "memory:" or "tools:" key  --  decode those into their target
// sub-struct directly.
func loadFeatureFile(cfg *Config, feature, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	switch feature {
	case "memory":
		if err := yaml.Unmarshal(data, &cfg.Memory); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	case "tools":
		if err := yaml.Unmarshal(data, &cfg.Tools); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	default:
		// models, gateway, identities  --  these have top-level keys that
		// match Config fields directly.
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	return nil
}
