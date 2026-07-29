package configuration

import (
	"sort"
	"strings"
)

// Feature names a recognised config.<feature>.yaml section.
type Feature string

const (
	FeatureGateway    Feature = "gateway"
	FeatureTools      Feature = "tools"
	FeatureMemory     Feature = "memory"
	FeatureModels     Feature = "models"
	FeatureIdentities Feature = "identities"
)

// knownFeatures is the closed set of recognised feature files. A
// config.<name>.yaml outside this set is an error rather than a silent
// no-op, so a misspelled filename does not look like a disabled feature.
var knownFeatures = map[Feature]bool{
	FeatureGateway:    true,
	FeatureTools:      true,
	FeatureMemory:     true,
	FeatureModels:     true,
	FeatureIdentities: true,
}

// known reports whether f is a recognised feature.
func (f Feature) known() bool { return knownFeatures[f] }

// featureList renders the recognised names for error messages. Derived from
// knownFeatures so the message cannot drift from the set it describes -- it
// was previously spelled out as a literal beside the map.
func featureList() string {
	names := make([]string, 0, len(knownFeatures))
	for name := range knownFeatures {
		names = append(names, string(name))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// featureFromFilename extracts the feature from a "config.<feature>.yaml"
// filename, or "" if the name does not have that shape.
func featureFromFilename(name string) Feature {
	if !strings.HasPrefix(name, "config.") || !isYAML(name) {
		return ""
	}
	return Feature(trimYAMLSuffix(strings.TrimPrefix(name, "config.")))
}

// isYAML reports whether name has a YAML extension.
func isYAML(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// isTOML reports whether name has a TOML extension.
func isTOML(name string) bool { return strings.HasSuffix(name, ".toml") }

// trimYAMLSuffix removes a .yaml or .yml extension.
func trimYAMLSuffix(name string) string {
	return strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml")
}
