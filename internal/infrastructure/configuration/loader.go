package configuration

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/config"
)

// Document is the decoded external configuration together with a record of
// where it came from.
type Document struct {
	// Config is the translated settings.
	//
	// TEMPORARY. docs/architecture/configuration.md requires this to become
	// typed per-domain sections that never expose a whole-application model.
	// It stays for now so this package can own loading without every
	// consumer changing at once. Delete when internal/config is dissolved.
	Config config.Config

	// Provenance lists the files that produced Config, in precedence order.
	Provenance Provenance
}

// Loader reads configuration from files. The zero value is not usable; call
// [New].
type Loader struct {
	log *slog.Logger

	// dataHomeFn resolves the base directory for derived paths. Injected so
	// tests can exercise defaulting without depending on the environment --
	// the previous package-level dataHome() read XDG_DATA_HOME directly and
	// discarded the error from os.UserHomeDir.
	dataHomeFn func() string
}

// New returns a Loader. A nil logger discards output.
func New(log *slog.Logger) *Loader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Loader{log: log, dataHomeFn: xdgDataHome}
}

// dataHome resolves the base directory for derived paths.
func (l *Loader) dataHome() string {
	if l.dataHomeFn == nil {
		return xdgDataHome()
	}
	return l.dataHomeFn()
}

// xdgDataHome returns $XDG_DATA_HOME, falling back to ~/.local/share, and
// finally to a relative path when the home directory is unknowable.
func xdgDataHome() string {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share")
	}
	return filepath.Join(home, ".local", "share")
}

// File loads a single configuration file. TOML remains supported for existing
// deployments; YAML files use the same defaults and validation path.
func (l *Loader) File(path string) (*Document, error) {
	return l.Resolve(path, "")
}

// Resolve selects the configuration source from its filesystem type: a TOML
// or YAML file, or a feature-config directory. This is the production entry
// point. Keeping selection here ensures command-line loading and operator
// tooling share the exact same precedence and validation rules.
func (l *Loader) Resolve(basePath, overlayPath string) (*Document, error) {
	info, err := os.Stat(basePath)
	if err != nil {
		return nil, fmt.Errorf("%w: config source %s: %w", ErrUnreadable, basePath, err)
	}
	if info.IsDir() {
		if overlayPath != "" {
			overlayInfo, statErr := os.Stat(overlayPath)
			if statErr != nil {
				return nil, fmt.Errorf("%w: config overlay %s: %w", ErrUnreadable, overlayPath, statErr)
			}
			if !overlayInfo.IsDir() {
				return nil, fmt.Errorf("config overlay %s must be a directory when config source %s is a directory", overlayPath, basePath)
			}
		}
		return l.Dir(basePath, overlayPath)
	}
	if overlayPath != "" {
		overlayInfo, statErr := os.Stat(overlayPath)
		if statErr != nil {
			return nil, fmt.Errorf("%w: config overlay %s: %w", ErrUnreadable, overlayPath, statErr)
		}
		if overlayInfo.IsDir() {
			return nil, fmt.Errorf("config overlay %s must be a file when config source %s is a file", overlayPath, basePath)
		}
	}
	return l.overlayFile(basePath, overlayPath)
}

// Overlay loads basePath, then decodes overlayPath into the same value so
// only the fields the overlay sets are replaced -- every field it omits keeps
// the base value. This lets a deployment-specific file (config.docker.toml)
// declare only what differs instead of restating the whole schema. An empty
// overlayPath is equivalent to [Loader.File].
func (l *Loader) Overlay(basePath, overlayPath string) (*Document, error) {
	return l.overlayFile(basePath, overlayPath)
}

func (l *Loader) overlayFile(basePath, overlayPath string) (*Document, error) {
	doc := &Document{}

	if err := decodeConfigFile(basePath, &doc.Config); err != nil {
		return nil, err
	}
	doc.Provenance.record(Origin{Path: basePath, Role: RoleMain, Layer: LayerBase})

	if overlayPath != "" {
		if err := decodeConfigFile(overlayPath, &doc.Config); err != nil {
			return nil, err
		}
		doc.Provenance.record(Origin{Path: overlayPath, Role: RoleMain, Layer: LayerOverlay})
	}

	return l.finalize(doc)
}

// Dir loads configuration from a directory tree:
//
//	config.yaml             daemon-level settings
//	config.gateway.yaml     chat channels and platforms
//	config.tools.yaml       MCP servers and tool policy
//	config.memory.yaml      memory provider settings
//	config.models.yaml      LLM providers and models
//	config.identities.yaml  identity settings
//	conf.d/*.yaml           additional feature files
//
// A missing feature file means the feature is absent, not an error. Legacy
// config.toml is the fallback when config.yaml is absent; YAML wins when
// both exist.
//
// When overlayDir is non-empty its files are applied on top of baseDir, with
// the same field-level precedence as [Loader.Overlay]. The overlay directory
// need not contain a main config.
func (l *Loader) Dir(baseDir, overlayDir string) (*Document, error) {
	doc := &Document{}

	if err := l.loadDir(doc, baseDir, LayerBase); err != nil {
		return nil, err
	}
	if overlayDir != "" {
		if err := l.loadDir(doc, overlayDir, LayerOverlay); err != nil {
			return nil, err
		}
	}

	return l.finalize(doc)
}

// loadDir discovers and decodes one directory into doc. The base layer must
// supply a main config; an overlay may contribute feature files alone.
func (l *Loader) loadDir(doc *Document, dir string, layer Layer) error {
	files, err := discover(dir)
	if err != nil {
		return err
	}

	switch path, isYAMLFile, ok := files.main(); {
	case ok:
		if err := l.decodeMain(&doc.Config, path, isYAMLFile); err != nil {
			return err
		}
		doc.Provenance.record(Origin{Path: path, Role: RoleMain, Layer: layer})
	case layer == LayerBase:
		return fmt.Errorf("%w: no config.yaml or config.toml in %s", ErrNoConfigFile, dir)
	}

	for _, feature := range files.sortedFeatures() {
		path := files.features[feature]
		if err := decodeFeature(&doc.Config, feature, path); err != nil {
			return err
		}
		doc.Provenance.record(Origin{Path: path, Role: RoleFeature, Layer: layer, Feature: feature})
	}

	for _, name := range files.sortedExtras() {
		path := files.extras[name]
		if err := decodeExtra(&doc.Config, name, path); err != nil {
			return err
		}
		doc.Provenance.record(Origin{Path: path, Role: RoleExtra, Layer: layer, Feature: Feature(name)})
	}

	return nil
}

// decodeMain decodes the daemon-level file in whichever format it uses.
func (l *Loader) decodeMain(cfg *config.Config, path string, isYAMLFile bool) error {
	if isYAMLFile {
		return decodeYAML(path, cfg)
	}
	return decodeTOML(path, cfg)
}

// finalize applies defaults, then validates. The order matters: validation
// judges the effective configuration, including values the operator never
// wrote.
func (l *Loader) finalize(doc *Document) (*Document, error) {
	l.applyDefaults(&doc.Config)
	if err := Validate(&doc.Config); err != nil {
		return nil, err
	}
	l.log.Debug("configuration loaded", "sources", doc.Provenance.String())
	return doc, nil
}
