package configuration

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/samcharles93/archie-core/internal/config"
)

// Document is the decoded external configuration together with a record of
// where it came from.
type Document struct {
	// Config is the translated settings.
	//
	// TEMPORARY. It stays for now so this package can own loading without every
	// consumer changing at once. Delete when internal/config is dissolved.
	Config config.Config
	// Scheduling is the decoded [scheduling] input. Application composition
	// translates it into domain-owned scheduling.EngineConfig.
	Scheduling SchedulingInput

	// Provenance lists the files that produced Config, in precedence order.
	Provenance Provenance

	// UnknownKeys lists sorted TOML key paths no decode target consumed.
	UnknownKeys []string
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

// xdgConfigHome returns $XDG_CONFIG_HOME, else ~/.config, else a relative
// path.
func xdgConfigHome() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".config"
	}
	return filepath.Join(home, ".config")
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
	if err := validateOverlayPath(overlayPath, basePath, info.IsDir()); err != nil {
		return nil, err
	}
	if info.IsDir() {
		return l.Dir(basePath, overlayPath)
	}
	return l.overlayFile(basePath, overlayPath)
}

// validateOverlayPath checks that overlayPath, when non-empty, exists and has
// the same filesystem kind as basePath: a directory overlay for a directory
// source and a file overlay for a file source.
func validateOverlayPath(overlayPath, basePath string, baseIsDir bool) error {
	if overlayPath == "" {
		return nil
	}
	overlayInfo, err := os.Stat(overlayPath)
	if err != nil {
		return fmt.Errorf("%w: config overlay %s: %w", ErrUnreadable, overlayPath, err)
	}
	if overlayInfo.IsDir() != baseIsDir {
		if baseIsDir {
			return fmt.Errorf("config overlay %s must be a directory when config source %s is a directory", overlayPath, basePath)
		}
		return fmt.Errorf("config overlay %s must be a file when config source %s is a file", overlayPath, basePath)
	}
	return nil
}

// Overlay loads basePath, then replaces only the fields overlayPath sets. An
// empty overlayPath is equivalent to File.
func (l *Loader) Overlay(basePath, overlayPath string) (*Document, error) {
	return l.overlayFile(basePath, overlayPath)
}

// ApplyOverlay layers runtime overrides over a copy of doc, then defaults and
// validates. doc is not mutated.
func (l *Loader) ApplyOverlay(doc *Document, overrides map[string]any) (*Document, error) {
	next := *doc
	next.Config = doc.Config.Clone()
	next.Provenance.Origins = append([]Origin(nil), doc.Provenance.Origins...)
	if len(overrides) == 0 {
		return l.finalize(&next, Validate)
	}
	if err := ApplyOverlayValues(&next.Config, overrides); err != nil {
		return nil, err
	}
	if err := applySchedulingOverlay(&next.Scheduling, overrides); err != nil {
		return nil, err
	}
	next.Provenance.record(Origin{Path: "config_overlay (runtime)", Role: RoleMain, Layer: LayerOverlay})
	return l.finalize(&next, Validate)
}

func (l *Loader) overlayFile(basePath, overlayPath string) (*Document, error) {
	doc := &Document{}

	cfgKeys, err := decodeConfigFileKeys(basePath, &doc.Config)
	if err != nil {
		return nil, err
	}
	schedKeys, err := decodeSchedulingFileKeys(basePath, &doc.Scheduling)
	if err != nil {
		return nil, err
	}
	doc.UnknownKeys = append(doc.UnknownKeys, unknownKeys(cfgKeys, schedKeys)...)
	doc.Provenance.record(Origin{Path: basePath, Role: RoleMain, Layer: LayerBase})

	if overlayPath != "" {
		// The overlay is folded over the base config rather than decoded into
		// it: a decode replaces a map-valued entry wholesale, clearing the
		// fields of that entry the overlay does not name (applyOverlayFile).
		cfgKeys, err = applyOverlayFile(overlayPath, &doc.Config)
		if err != nil {
			return nil, err
		}
		schedKeys, err = decodeSchedulingFileKeys(overlayPath, &doc.Scheduling)
		if err != nil {
			return nil, err
		}
		doc.UnknownKeys = append(doc.UnknownKeys, unknownKeys(cfgKeys, schedKeys)...)
		doc.Provenance.record(Origin{Path: overlayPath, Role: RoleMain, Layer: LayerOverlay})
	}

	return l.finalize(doc, validateBootstrap)
}

// overlayFileKeys returns the overlay file's keys that neither the TOML
// decode nor the overlay apply consumes.
func overlayFileKeys(path string, mapping map[string]any, target reflect.Type) ([]string, error) {
	decoded, err := decodeConfigFileKeys(path, reflect.New(target).Interface())
	if err != nil {
		return nil, err
	}
	return append(decoded, unmatchableKeys(mapping, target)...), nil
}

// unmatchableKeys returns the keys in mapping that match no field of target.
func unmatchableKeys(mapping map[string]any, target reflect.Type) []string {
	var out []string
	for key, value := range mapping {
		field, ok := yamlFieldOf(target, key)
		if !ok {
			out = append(out, key)
			continue
		}
		sub, ok := value.(map[string]any)
		if !ok {
			continue
		}
		switch field.Type.Kind() {
		case reflect.Struct:
			out = append(out, prefixed(key, unmatchableKeys(sub, field.Type))...)
		case reflect.Map:
			entry := field.Type.Elem()
			for entry.Kind() == reflect.Pointer {
				entry = entry.Elem()
			}
			if entry.Kind() != reflect.Struct || field.Type.Key().Kind() != reflect.String {
				continue
			}
			for entryName, entryValue := range sub {
				entryMapping, ok := entryValue.(map[string]any)
				if !ok {
					continue
				}
				out = append(out, prefixed(key+"."+entryName, unmatchableKeys(entryMapping, entry))...)
			}
		}
	}
	return out
}

// prefixed qualifies a nested key report with the path that reached it.
func prefixed(prefix string, keys []string) []string {
	for i, key := range keys {
		keys[i] = prefix + "." + key
	}
	return keys
}

// unknownKeys returns the keys present in both a and b, i.e. consumed by
// neither decode target.
func unknownKeys(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	inB := make(map[string]bool, len(b))
	for _, k := range b {
		inB[k] = true
	}
	var out []string
	for _, k := range a {
		if inB[k] {
			out = append(out, k)
		}
	}
	return out
}

// unregisteredServiceKeys returns [services.<name>] sections for unregistered
// services.
func unregisteredServiceKeys(services config.Services) []string {
	var out []string
	for name := range services {
		if _, ok := config.LookupService(name); !ok {
			out = append(out, "services."+name)
		}
	}
	return out
}

// sortedUnique returns keys sorted and de-duplicated, so Document.UnknownKeys
// is deterministic across a base+overlay load that happens to name the same
// stray key in both files.
func sortedUnique(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Dir loads configuration from a directory:
//
//	config.yaml             daemon-level settings
//	config.gateway.yaml     chat channels and platforms
//	config.tools.yaml       MCP servers and tool policy
//	config.memory.yaml      memory provider settings
//	config.models.yaml      LLM providers and models
//	config.identities.yaml  identity settings
//	conf.d/*.yaml           additional feature files
//
// Missing feature files are fine. config.toml is used when config.yaml is
// absent. A non-empty overlayDir is applied on top.
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

	return l.finalize(doc, validateBootstrap)
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
		if err := l.decodeMain(doc, path, isYAMLFile, layer); err != nil {
			return err
		}
		doc.Provenance.record(Origin{Path: path, Role: RoleMain, Layer: layer})
	case layer == LayerBase:
		return fmt.Errorf("%w: no config.yaml or config.toml in %s", ErrNoConfigFile, dir)
	}

	for _, feature := range files.sortedFeatures() {
		path := files.features[feature]
		if err := decodeFeature(&doc.Config, feature, path, layer); err != nil {
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
func (l *Loader) decodeMain(doc *Document, path string, isYAMLFile bool, layer Layer) error {
	if layer == LayerOverlay {
		cfgKeys, err := applyOverlayFile(path, &doc.Config)
		if err != nil {
			return err
		}
		doc.UnknownKeys = append(doc.UnknownKeys, cfgKeys...)
		return decodeSchedulingFile(path, &doc.Scheduling)
	}
	if isYAMLFile {
		if err := decodeYAML(path, &doc.Config); err != nil {
			return err
		}
		return decodeSchedulingFile(path, &doc.Scheduling)
	}
	cfgKeys, err := decodeTOMLKeys(path, &doc.Config)
	if err != nil {
		return err
	}
	schedKeys, err := decodeSchedulingFileKeys(path, &doc.Scheduling)
	if err != nil {
		return err
	}
	doc.UnknownKeys = append(doc.UnknownKeys, unknownKeys(cfgKeys, schedKeys)...)
	return nil
}

// finalize applies defaults, then validate.
func (l *Loader) finalize(doc *Document, validate func(*config.Config) error) (*Document, error) {
	doc.UnknownKeys = sortedUnique(append(doc.UnknownKeys, unregisteredServiceKeys(doc.Config.Services)...))
	l.applyDefaults(&doc.Config)
	if err := validate(&doc.Config); err != nil {
		return nil, err
	}
	l.log.Debug("configuration loaded", "sources", doc.Provenance.String())
	return doc, nil
}
