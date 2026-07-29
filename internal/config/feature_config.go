package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// knownFeatureList renders the recognised feature names for error messages.
// Derived from knownFeatureNames so the two cannot drift when a feature is
// added.
func knownFeatureList() string {
	names := make([]string, 0, len(knownFeatureNames))
	for name := range knownFeatureNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
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

	mainYAML, mainTOML, featurePaths, extraPaths, err := scanConfigEntries(dir, entries)
	if err != nil {
		return err
	}

	if err := loadMainConfig(cfg, mainYAML, mainTOML, requireMain); err != nil {
		return err
	}
	if err := loadFeatureConfigs(cfg, featurePaths); err != nil {
		return err
	}
	loadExtraConfigs(cfg, extraPaths)
	return nil
}

// scanConfigEntries walks dir entries and classifies them into main config,
// feature, and extra paths.
func scanConfigEntries(dir string, entries []os.DirEntry) (mainYAML, mainTOML string, featurePaths, extraPaths map[string]string, err error) {
	featurePaths = map[string]string{}
	extraPaths = map[string]string{}
	var unknownConfigFiles []string

	for _, e := range entries {
		if e.IsDir() {
			if e.Name() == "conf.d" {
				if err := scanConfDir(dir, featurePaths, extraPaths); err != nil {
					return "", "", nil, nil, err
				}
			}
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".toml") {
			continue
		}
		switch {
		case name == "config.yaml" || name == "config.yml":
			mainYAML = filepath.Join(dir, name)
		case name == "config.toml":
			mainTOML = filepath.Join(dir, name)
		default:
			unknown, classifyErr := classifyFeatureFile(dir, name, featurePaths)
			if classifyErr != nil {
				return "", "", nil, nil, classifyErr
			}
			if unknown != "" {
				unknownConfigFiles = append(unknownConfigFiles, unknown)
			}
		}
	}
	if len(unknownConfigFiles) > 0 {
		return "", "", nil, nil, fmt.Errorf("unrecognized config file %q (known features: %s)", unknownConfigFiles[0], knownFeatureList())
	}
	return
}

// classifyFeatureFile registers a feature file in featurePaths.
//
// It returns the filename when the feature is unrecognised, so the caller can
// report all such files together, and an error when two files claim the same
// feature: silently preferring one would apply settings the operator cannot
// see from the filenames alone.
func classifyFeatureFile(dir, name string, featurePaths map[string]string) (string, error) {
	if !strings.HasPrefix(name, "config.") || (!strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml")) {
		return "", nil
	}
	feature := configFeatureName(name)
	if !knownFeatureNames[feature] {
		return name, nil
	}
	path := filepath.Join(dir, name)
	if existing, ok := featurePaths[feature]; ok {
		return "", fmt.Errorf("duplicate feature %q: both %s and %s define it", feature, existing, path)
	}
	featurePaths[feature] = path
	return "", nil
}

func configFeatureName(filename string) string {
	rest := strings.TrimPrefix(filename, "config.")
	return strings.TrimSuffix(strings.TrimSuffix(rest, ".yaml"), ".yml")
}

func loadMainConfig(cfg *Config, mainYAML, mainTOML string, requireMain bool) error {
	if mainYAML != "" {
		data, err := os.ReadFile(mainYAML)
		if err != nil {
			return fmt.Errorf("reading %s: %w", mainYAML, err)
		}
		return yaml.Unmarshal(data, cfg)
	}
	if mainTOML != "" {
		_, err := toml.DecodeFile(mainTOML, cfg)
		return err
	}
	if requireMain {
		return fmt.Errorf("no config.yaml or config.toml found in %s", "")
	}
	return nil
}

func loadFeatureConfigs(cfg *Config, featurePaths map[string]string) error {
	for feature, path := range featurePaths {
		if err := loadFeatureFile(cfg, feature, path); err != nil {
			return err
		}
	}
	return nil
}

func loadExtraConfigs(cfg *Config, extraPaths map[string]string) {
	for name, path := range extraPaths {
		data, err := os.ReadFile(path)
		if err != nil { // skip unreadable extras silently
			continue
		}
		var val any
		if err := yaml.Unmarshal(data, &val); err != nil {
			continue
		}
		if cfg.Extra == nil {
			cfg.Extra = make(map[string]any)
		}
		cfg.Extra[name] = val
	}
}

// scanConfDir reads YAML files from dir/conf.d/ and distributes them into
// featurePaths (known features) or extraPaths (unknown).
func scanConfDir(dir string, featurePaths, extraPaths map[string]string) error {
	confEntries, err := os.ReadDir(filepath.Join(dir, "conf.d"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading conf.d: %w", err)
	}
	for _, ce := range confEntries {
		cn := ce.Name()
		if ce.IsDir() || (!strings.HasSuffix(cn, ".yaml") && !strings.HasSuffix(cn, ".yml")) {
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
	return nil
}

// loadFeatureFile decodes a feature file's YAML into the correct part of
// Config. Most feature files map to top-level Config fields and can be
// decoded with yaml.Unmarshal(data, cfg) directly. Memory and tools are
// sub-structs whose YAML keys live at the top level of the file, not nested
// under a "memory:" or "tools:" key  --  decode those into their target
// sub-struct directly.
func loadFeatureFile(cfg *Config, feature, path string) error {
	if cfg == nil {
		return fmt.Errorf("config must not be nil")
	}
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
