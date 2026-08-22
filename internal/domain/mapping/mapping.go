// Package mapping owns the payload field-mapping vocabulary and its
// resolve/preview rule: binding named fields to JSON paths inside a
// captured webhook payload, and deciding -- loudly -- when a path a mapping
// depends on has drifted out from under it. See
// docs/prds/payload-field-mapping.md.
package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FieldType is the expected JSON shape of a mapped field, checked at
// resolve time.
type FieldType string

const (
	TypeString FieldType = "string"
	TypeNumber FieldType = "number"
	TypeBool   FieldType = "bool"
	TypeObject FieldType = "object"
	TypeArray  FieldType = "array"
	TypeAny    FieldType = "any"
)

// Failure reasons. A field either resolves to a value or fails for exactly
// one of these -- never both.
const (
	ReasonMissing      = "missing"
	ReasonNull         = "null"
	ReasonTypeMismatch = "type_mismatch"
)

// Field binds one named field the workflow receives to a path into a
// captured payload. Required=false suppresses ReasonMissing only -- a
// present-but-wrong-shape value always fails, so a mapping never silently
// hands an agent a value it didn't expect.
type Field struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Type     FieldType `json:"type"`
	Required bool      `json:"required"`
}

// Mapping is a named, reusable set of field bindings, authored by clicking
// through one example captured event but not pinned to that event or its
// source -- t2db.4's matcher decides which events a binding applies to.
type Mapping struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	SourceHint string    `json:"source_hint"`
	Fields     []Field   `json:"fields"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// validTypes is the set of FieldType values Validate accepts.
var validTypes = map[FieldType]bool{
	TypeString: true, TypeNumber: true, TypeBool: true,
	TypeObject: true, TypeArray: true, TypeAny: true,
}

// Validate checks a mapping is well-formed before it is persisted or
// previewed: a name, at least one field, and every field naming a
// non-empty field name, a non-empty path, and a recognised type. It does
// not check the mapping against any payload -- that is Resolve's job.
func (m Mapping) Validate() error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("mapping: name is required")
	}
	if len(m.Fields) == 0 {
		return fmt.Errorf("mapping: at least one field is required")
	}
	seen := make(map[string]bool, len(m.Fields))
	for i, f := range m.Fields {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("mapping: field %d: name is required", i)
		}
		if strings.TrimSpace(f.Path) == "" {
			return fmt.Errorf("mapping: field %d (%s): path is required", i, f.Name)
		}
		if !validTypes[f.Type] {
			return fmt.Errorf("mapping: field %d (%s): unrecognised type %q", i, f.Name, f.Type)
		}
		// A duplicate name would let one field silently overwrite another's
		// resolved value in Resolve's values map -- reject it here instead
		// of losing data silently at resolve time.
		if seen[f.Name] {
			return fmt.Errorf("mapping: field %d: duplicate field name %q", i, f.Name)
		}
		seen[f.Name] = true
	}
	return nil
}

// Failure describes one field that did not resolve cleanly against a
// payload -- named by field and path so the operator (or, once t2db.4
// lands, the runtime failure it surfaces) knows exactly what drifted.
type Failure struct {
	FieldName string `json:"field_name"`
	Path      string `json:"path"`
	Reason    string `json:"reason"`
}

// Resolve walks payload independently for each field, so one field's
// failure never stops the others from resolving -- preview must show every
// problem at once, not the first one. A field that fails never appears in
// values.
func Resolve(fields []Field, payload []byte) (values map[string]any, failures []Failure) {
	values = make(map[string]any, len(fields))

	var root any
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		for _, f := range fields {
			failures = append(failures, Failure{FieldName: f.Name, Path: f.Path, Reason: ReasonMissing})
		}
		return values, failures
	}

	for _, f := range fields {
		v, found := walk(root, parsePath(f.Path))
		if !found {
			if f.Required {
				failures = append(failures, Failure{FieldName: f.Name, Path: f.Path, Reason: ReasonMissing})
			}
			continue
		}
		if v == nil {
			failures = append(failures, Failure{FieldName: f.Name, Path: f.Path, Reason: ReasonNull})
			continue
		}
		if !typeMatches(v, f.Type) {
			failures = append(failures, Failure{FieldName: f.Name, Path: f.Path, Reason: ReasonTypeMismatch})
			continue
		}
		values[f.Name] = normalize(v)
	}

	return values, failures
}

// pathSegment is a map key (Key set, Index == -1), an array index
// (Index >= 0, Key empty), or Invalid -- a bracket segment whose contents
// were not a plain non-negative integer (e.g. "items[abc]"). walk always
// fails an Invalid segment rather than silently dropping it and matching a
// shorter, unintended path -- a malformed path must read as ReasonMissing,
// never as a different path succeeding by accident.
type pathSegment struct {
	Key     string
	Index   int
	Invalid bool
}

// parsePath splits "data.items[0].id" into
// [{Key:"data"} {Key:"items"} {Index:0} {Key:"id"}]. No wildcards, no
// filters -- the grammar is exactly what a click-through UI generates.
func parsePath(path string) []pathSegment {
	var segs []pathSegment
	for part := range strings.SplitSeq(path, ".") {
		if part == "" {
			continue
		}
		for {
			open := strings.IndexByte(part, '[')
			if open < 0 {
				if part != "" {
					segs = append(segs, pathSegment{Key: part, Index: -1})
				}
				break
			}
			if open > 0 {
				segs = append(segs, pathSegment{Key: part[:open], Index: -1})
			}
			closeIdx := strings.IndexByte(part[open:], ']')
			if closeIdx < 0 {
				segs = append(segs, pathSegment{Invalid: true})
				break
			}
			closeIdx += open
			idx, err := strconv.Atoi(part[open+1 : closeIdx])
			if err != nil {
				segs = append(segs, pathSegment{Invalid: true})
			} else {
				segs = append(segs, pathSegment{Index: idx})
			}
			part = part[closeIdx+1:]
			if part == "" {
				break
			}
		}
	}
	return segs
}

// walk descends node by path. found=false means the path does not exist in
// the payload at all (missing), which is distinct from resolving to a JSON
// null (node == nil, found == true).
func walk(node any, path []pathSegment) (value any, found bool) {
	cur := node
	for _, seg := range path {
		if seg.Invalid {
			return nil, false
		}
		if cur == nil {
			return nil, false
		}
		if seg.Index >= 0 {
			arr, ok := cur.([]any)
			if !ok || seg.Index >= len(arr) {
				return nil, false
			}
			cur = arr[seg.Index]
			continue
		}
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, ok := obj[seg.Key]
		if !ok {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func typeMatches(v any, want FieldType) bool {
	if want == TypeAny {
		return true
	}
	switch v.(type) {
	case string:
		return want == TypeString
	case json.Number:
		return want == TypeNumber
	case bool:
		return want == TypeBool
	case map[string]any:
		return want == TypeObject
	case []any:
		return want == TypeArray
	default:
		return false
	}
}

// normalize converts json.Number to float64 so resolved values are ordinary
// JSON-encodable Go types for the preview response, matching
// encoding/json's own default number decoding.
func normalize(v any) any {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return fmt.Sprint(n)
		}
		return f
	}
	return v
}
