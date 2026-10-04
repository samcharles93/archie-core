package controlplane

import (
	"encoding/json"
	"maps"
	"reflect"
	"strings"
	"time"
)

// bareObjectSchema is what a descriptor advertises when its definition names no
// document. It is the fallback, not the norm: a descriptor with no properties is
// what left the Web UI with nothing to label, describe or format a field with
const bareObjectSchema = `{"type":"object"}`

var timeType = reflect.TypeFor[time.Time]()

// stdDuration is the marker this tree's string-form duration types satisfy:
// config.Duration, channelDuration and scheduling.Duration each expose the
// standard duration for arithmetic.
var stdDuration = reflect.TypeOf((*interface{ Std() time.Duration })(nil)).Elem()

// durationLike reports whether t marshals as a duration string.
func durationLike(t reflect.Type) bool {
	if !t.Implements(stdDuration) {
		return false
	}
	encoded, err := json.Marshal(reflect.Zero(t).Interface())
	return err == nil && len(encoded) > 0 && encoded[0] == '"'
}

// schemaJSON derives a resource's JSON Schema from its document type: keys,
// types and tag annotations. It never emits required.
func schemaJSON(document any, extensions map[string]any) string {
	if document == nil {
		return bareObjectSchema
	}
	schema := schemaOf(reflect.TypeOf(document))
	maps.Copy(schema, extensions)
	encoded, err := json.Marshal(schema)
	if err != nil {
		// Unreachable: the tree below holds only strings, maps, slices and
		// booleans. A boot must not fail over documentation, so degrade to the
		// shape descriptors carried before this existed rather than panic.
		return bareObjectSchema
	}
	return string(encoded)
}

// schemaOf is the JSON Schema for one type, following json tags for keys and the
// Go type for everything else.
func schemaOf(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case durationLike(t):
		// The type writes the string time.ParseDuration reads, so the schema says
		// so and a client can offer a duration affordance instead of a number
		// box. This rule reaches every duration field in the catalogue, not just
		// the one that prompted it.
		return map[string]any{"type": "string", "format": "duration"}
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Struct:
		return objectSchemaOf(t)
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "string"}
	}
}

func objectSchemaOf(t reflect.Type) map[string]any {
	properties := map[string]any{}
	for field := range t.Fields() {
		if field.Anonymous {
			// encoding/json promotes an embedded struct's fields into the
			// enclosing object, so the schema lists them there too. Dropping
			// them would leave document keys nothing describes.
			embedded, ok := schemaOf(field.Type)["properties"].(map[string]any)
			if !ok {
				continue
			}
			maps.Copy(properties, embedded)
			continue
		}
		if field.PkgPath != "" {
			continue // unexported: not part of any document
		}
		name, ok := documentKey(field)
		if !ok {
			continue
		}
		properties[name] = documented(schemaOf(field.Type), field)
	}
	return map[string]any{"type": "object", "properties": properties}
}

// documentKey is the key a field carries in its document: the json tag's name,
// or the Go field name when the tag declares none, because that fallback is what
// encoding/json uses and therefore what the document really holds. Reporting the
// tag instead of the fallback would describe a document nobody stores.
func documentKey(field reflect.StructField) (string, bool) {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	switch name {
	case "-":
		return "", false
	case "":
		return field.Name, true
	default:
		return name, true
	}
}

// documented layers the three annotations a field declares in its own tags over
// the derived type.
func documented(schema map[string]any, field reflect.StructField) map[string]any {
	for _, annotation := range []struct{ tag, key string }{
		{"title", "title"},
		{"format", "format"},
		{"doc", "description"},
	} {
		if value := field.Tag.Get(annotation.tag); value != "" {
			schema[annotation.key] = value
		}
	}
	return schema
}
