package configuration

import (
	"fmt"
	"maps"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/samcharles93/archie-core/internal/config"
)

// ApplyOverlayValues decodes overrides (a nested map of dotted-path
// values, as produced by the overlay store) into cfg using the same
// field-level precedence as a file overlay: only the keys present are
// replaced, every field the overlay omits keeps its existing value.
//
// This is the decode half of the runtime config overlay. The loader's
// ApplyOverlay wraps it with defaulting, validation and provenance; the
// dashboard PATCH path calls it on a copy of the published config and
// validates the materialised result before persisting. Either way the
// caller owns the cfg value -- pass a copy when the base must survive.
//
// overrides is read, never mutated.
func ApplyOverlayValues(cfg *config.Config, overrides map[string]any) error {
	if len(overrides) == 0 {
		return nil
	}
	return applyOverlayMapping(cfg, overrides)
}

// applyOverlayFile layers the overlay file at path over cfg, with the same
// field-level precedence ApplyOverlayValues gives a map overlay. The file
// overlay path needs it for the reason recorded on foldOverrides: decoding an
// overlay into cfg replaces a map-valued entry wholesale, so a file naming
// only services.state.target cleared the target_token the base file set. Both
// overlay paths go through the fold, so neither can drift from the other.
func applyOverlayFile(path string, cfg *config.Config) error {
	doc, err := decodeFileMapping(path)
	if err != nil {
		return err
	}
	return applyOverlayMapping(cfg, doc)
}

// applyOverlayMapping is the decode both overlay paths share: the fields each
// map-valued entry the overlay does not name are folded forward out of the
// entry cfg already holds, and the completed mapping is then decoded over cfg.
//
// yaml gives a struct field the field-level treatment ApplyOverlayValues
// promises, but a map VALUE is decoded into a fresh zero value: an entry the
// overlay only partially addresses would lose every field it does not name, so
// an operator changing services.state.target alone would silently drop
// target_token. Folding here, once, is what makes the promise hold for every
// map of structs (services, image.hosted, image.local, providers) instead of
// at each field's reader.
func applyOverlayMapping(cfg *config.Config, doc map[string]any) error {
	folded, err := foldOverrides(reflect.ValueOf(cfg), doc)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(folded)
	if err != nil {
		return fmt.Errorf("%w: encoding config overlay: %w", ErrUnreadable, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("%w: parsing config overlay: %w", ErrUnreadable, err)
	}
	return nil
}

// foldOverrides returns doc with the fields its map entries omit carried
// forward from the entries cfg already holds. It descends through structs, so
// {"image": {"hosted": {...}}} folds the same way as an override written at the
// top level.
//
// Only mapping-valued map entries need it: yaml keeps the entries a map of
// scalars does not name, and a struct field it does not name keeps its value.
func foldOverrides(cfg reflect.Value, doc map[string]any) (map[string]any, error) {
	if cfg.Kind() == reflect.Pointer {
		if cfg.IsNil() {
			return doc, nil
		}
		cfg = cfg.Elem()
	}
	if cfg.Kind() != reflect.Struct {
		return doc, nil
	}
	out := make(map[string]any, len(doc))
	maps.Copy(out, doc)
	for key, value := range out {
		field, ok := yamlField(cfg, key)
		if !ok {
			continue
		}
		sub, ok := value.(map[string]any)
		if !ok {
			continue // a scalar replaces the whole field, and the decode says so
		}
		switch field.Kind() {
		case reflect.Struct:
			folded, err := foldOverrides(field, sub)
			if err != nil {
				return nil, err
			}
			out[key] = folded
		case reflect.Map:
			folded, err := foldMapEntries(field, sub)
			if err != nil {
				return nil, err
			}
			out[key] = folded
		}
	}
	return out, nil
}

// foldMapEntries returns one map field's override with each entry the overlay
// names carried forward from the entry of the same key in cfg.
func foldMapEntries(field reflect.Value, doc map[string]any) (map[string]any, error) {
	if field.IsNil() || field.Type().Key().Kind() != reflect.String {
		return doc, nil
	}
	out := make(map[string]any, len(doc))
	maps.Copy(out, doc)
	for key, value := range out {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		existing := field.MapIndex(reflect.ValueOf(key).Convert(field.Type().Key()))
		if !existing.IsValid() {
			continue // an entry the overlay introduces has no earlier fields
		}
		carried, err := asMapping(existing)
		if err != nil {
			return nil, err
		}
		if carried == nil {
			continue // not a mapping, so the overlay replaces it wholesale
		}
		out[key] = mergeMapping(carried, entry)
	}
	return out, nil
}

// asMapping renders one config value as the mapping the decode will read it
// back as, which is what lets the fields an override omits be carried forward.
// It returns nil when the value does not encode to a mapping.
func asMapping(value reflect.Value) (map[string]any, error) {
	data, err := yaml.Marshal(value.Interface())
	if err != nil {
		return nil, fmt.Errorf("%w: encoding config overlay: %w", ErrUnreadable, err)
	}
	// Decoding into any cannot fail for marshalled output; a value that is not
	// a mapping simply yields no mapping.
	var generic any
	_ = yaml.Unmarshal(data, &generic)
	mapping, _ := generic.(map[string]any)
	return mapping, nil
}

// mergeMapping layers override over base: mappings merge key by key, anything
// else replaces, matching how the decode treats a struct field versus a scalar.
// It allocates rather than mutating either argument.
func mergeMapping(base, override map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(override))
	maps.Copy(out, base)
	for key, value := range override {
		if over, ok := value.(map[string]any); ok {
			if under, ok := base[key].(map[string]any); ok {
				out[key] = mergeMapping(under, over)
				continue
			}
		}
		out[key] = value
	}
	return out
}

// yamlField returns the field of struct v that the decode reads key into. The
// yaml tag is the one consulted because the decode below is yaml's.
func yamlField(v reflect.Value, key string) (reflect.Value, bool) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // unexported, so the decoder cannot write it
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		if name == key {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}
