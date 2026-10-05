package configuration

import (
	"fmt"
	"maps"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

// applyOverlayFile layers the overlay file at path over target and returns
// the file's unconsumed keys.
func applyOverlayFile(path string, target any) ([]string, error) {
	mapping, err := decodeFileMapping(path)
	if err != nil {
		return nil, err
	}
	if err := applyOverlayMapping(target, mapping); err != nil {
		return nil, err
	}
	return overlayFileKeys(path, mapping, structTypeOf(target))
}

// applyOverlayMapping decodes doc over target, first carrying forward the
// fields of map entries that doc only partly sets.
func applyOverlayMapping(target any, doc map[string]any) error {
	folded, err := foldOverrides(reflect.ValueOf(target), doc)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(folded)
	if err != nil {
		return fmt.Errorf("%w: encoding config overlay: %w", ErrUnreadable, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%w: parsing config overlay: %w", ErrUnreadable, err)
	}
	return nil
}

// structTypeOf returns the struct type target points at, so the fold and the key
// report can inspect it without an instance.
func structTypeOf(target any) reflect.Type {
	t := reflect.TypeOf(target)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// foldOverrides returns doc with the fields its map entries omit filled from
// cfg's existing entries, descending through structs.
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
	out := make(map[string]any, len(base))
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
	field, ok := yamlFieldOf(v.Type(), key)
	if !ok {
		return reflect.Value{}, false
	}
	return v.FieldByIndex(field.Index), true
}

// yamlFieldOf is yamlField without a value: the overlay key report walks a raw
// mapping against the target's type before anything is decoded into it.
func yamlFieldOf(t reflect.Type, key string) (reflect.StructField, bool) {
	for field := range t.Fields() {
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
			return field, true
		}
	}
	return reflect.StructField{}, false
}
