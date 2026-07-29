package configuration

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

// confDirName is the subdirectory holding additional feature files.
const confDirName = "conf.d"

// fileSet is the outcome of scanning one configuration directory: which file
// carries the daemon-level settings, which files carry recognised features,
// and which carry opaque extras.
//
// Scanning previously returned four bare values (two paths and two maps),
// which left the caller to remember that YAML wins over TOML and that extras
// are keyed by feature name.
type fileSet struct {
	mainYAML string
	mainTOML string
	features map[Feature]string
	extras   map[string]string
}

// main returns the daemon-level config file and its format. YAML wins when
// both are present; legacy config.toml is the fallback.
func (fs fileSet) main() (path string, yaml, ok bool) {
	if fs.mainYAML != "" {
		return fs.mainYAML, true, true
	}
	if fs.mainTOML != "" {
		return fs.mainTOML, false, true
	}
	return "", false, false
}

// sortedFeatures returns recognised features in a stable order, so loading
// is deterministic and provenance reads the same across runs. Map iteration
// order previously made the applied order arbitrary.
func (fs fileSet) sortedFeatures() []Feature {
	names := make([]Feature, 0, len(fs.features))
	for f := range fs.features {
		names = append(names, f)
	}
	slices.Sort(names)
	return names
}

// sortedExtras returns extra names in a stable order.
func (fs fileSet) sortedExtras() []string {
	names := make([]string, 0, len(fs.extras))
	for n := range fs.extras {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// discover scans dir and classifies its entries.
func discover(dir string) (fileSet, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fileSet{}, fmt.Errorf("%w: config dir %s: %w", ErrUnreadable, dir, err)
	}

	fs := fileSet{
		features: map[Feature]string{},
		extras:   map[string]string{},
	}
	var unknown []string

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if name == confDirName {
				if err := fs.scanConfDir(dir); err != nil {
					return fileSet{}, err
				}
			}
			continue
		}
		switch {
		case name == "config.yaml" || name == "config.yml":
			fs.mainYAML = filepath.Join(dir, name)
		case name == "config.toml":
			fs.mainTOML = filepath.Join(dir, name)
		case isYAML(name) || isTOML(name):
			if err := fs.classify(dir, name, &unknown); err != nil {
				return fileSet{}, err
			}
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fileSet{}, fmt.Errorf("%w: %q (known features: %s)", ErrUnknownFeature, unknown[0], featureList())
	}
	return fs, nil
}

// classify records a config.<feature>.yaml file, collecting unrecognised
// names into unknown so every one can be reported rather than just the first
// encountered.
func (fs fileSet) classify(dir, name string, unknown *[]string) error {
	feature := featureFromFilename(name)
	if feature == "" {
		return nil
	}
	if !feature.known() {
		*unknown = append(*unknown, name)
		return nil
	}
	return fs.addFeature(feature, filepath.Join(dir, name))
}

// scanConfDir reads dir/conf.d/, routing recognised names to features and
// the rest to extras.
func (fs fileSet) scanConfDir(dir string) error {
	confDir := filepath.Join(dir, confDirName)
	entries, err := os.ReadDir(confDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%w: %s: %w", ErrUnreadable, confDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isYAML(name) {
			continue
		}
		path := filepath.Join(confDir, name)
		feature := Feature(trimYAMLSuffix(name))
		if !feature.known() {
			fs.extras[string(feature)] = path
			continue
		}
		if err := fs.addFeature(feature, path); err != nil {
			return err
		}
	}
	return nil
}

// addFeature records a feature file, rejecting a second file claiming the
// same feature. Preferring either silently would apply settings the operator
// cannot predict from the filenames.
func (fs fileSet) addFeature(feature Feature, path string) error {
	if existing, ok := fs.features[feature]; ok {
		return fmt.Errorf("%w: %q defined by both %s and %s", ErrDuplicateFeature, feature, existing, path)
	}
	fs.features[feature] = path
	return nil
}
