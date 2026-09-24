package eventtype

import (
	"bytes"
	"encoding/json"
	"maps"
	"mime"
	"slices"
	"strings"
)

// ValueType is the JSON shape of one payload path. The names match
// mapping.FieldType so a mapping can be checked against a type's schema.
type ValueType string

const (
	TypeString ValueType = "string"
	TypeNumber ValueType = "number"
	TypeBool   ValueType = "bool"
	TypeObject ValueType = "object"
	TypeArray  ValueType = "array"
	TypeNull   ValueType = "null"
)

// Sample is one event as the rules see it: headers keyed by lower-case name
// (first value only) and the raw body.
type Sample struct {
	Headers map[string]string
	Body    []byte
}

// Signature is the structure of one event: every JSON path with its value
// type, plus the discriminator headers with their values. Array elements share
// one path segment, "[]", so two payloads differing only in list length have
// the same signature.
type Signature struct {
	Paths   map[string]ValueType `json:"paths"`
	Headers map[string]string    `json:"headers"`
}

// ParseHeaders reads a capture's stored headers (an encoded http.Header) into
// the lower-case, first-value form a Sample carries. Anything unreadable is no
// headers, not an error: a capture's headers are evidence, not input.
func ParseHeaders(raw string) map[string]string {
	out := map[string]string{}
	var h map[string][]string
	if json.Unmarshal([]byte(raw), &h) != nil {
		return out
	}
	for name, values := range h {
		if len(values) > 0 {
			out[strings.ToLower(name)] = values[0]
		}
	}
	return out
}

// isDiscriminator reports whether a header names the kind of event rather than
// describing its transport: X-GitHub-Event, X-Gitea-Event-Type, Content-Type.
func isDiscriminator(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, "-event") || strings.HasSuffix(name, "-type")
}

// headerValue normalises a header value for comparison. Content-Type compares
// by media type, so a charset parameter does not split one kind of event in two.
func headerValue(name, value string) string {
	value = strings.TrimSpace(value)
	if strings.EqualFold(name, "content-type") {
		if mt, _, err := mime.ParseMediaType(value); err == nil {
			return mt
		}
	}
	return value
}

// Sign computes an event's structural signature. A body that is not JSON has
// no paths; its headers still discriminate it.
func Sign(s Sample) Signature {
	sig := Signature{Paths: map[string]ValueType{}, Headers: map[string]string{}}
	for name, value := range s.Headers {
		if isDiscriminator(name) {
			sig.Headers[strings.ToLower(name)] = headerValue(name, value)
		}
	}
	if root, ok := decode(s.Body); ok {
		collectPaths(root, "", sig.Paths)
	}
	return sig
}

// Key is a stable string that is equal for two signatures exactly when their
// paths, path types and discriminator headers are equal.
func (s Signature) Key() string {
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(s.Headers)) {
		b.WriteString("h:" + name + "=" + s.Headers[name] + "\n")
	}
	for _, path := range slices.Sorted(maps.Keys(s.Paths)) {
		b.WriteString("p:" + path + "=" + string(s.Paths[path]) + "\n")
	}
	return b.String()
}

func decode(body []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	return v, true
}

func valueType(v any) ValueType {
	switch v.(type) {
	case string:
		return TypeString
	case json.Number:
		return TypeNumber
	case bool:
		return TypeBool
	case map[string]any:
		return TypeObject
	case []any:
		return TypeArray
	default:
		return TypeNull
	}
}

// collectPaths records every path below v. The root itself has no path.
func collectPaths(v any, prefix string, out map[string]ValueType) {
	switch typed := v.(type) {
	case map[string]any:
		for key, child := range typed {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			out[path] = valueType(child)
			collectPaths(child, path, out)
		}
	case []any:
		for _, child := range typed {
			path := prefix + "[]"
			out[path] = valueType(child)
			collectPaths(child, path, out)
		}
	}
}

// lookup returns every value at path; "[]" segments fan out over array
// elements, so a path inside a list yields one value per element that has it.
func lookup(root any, path string) []any {
	current := []any{root}
	for seg := range strings.SplitSeq(path, ".") {
		name, arrays := seg, 0
		for strings.HasSuffix(name, "[]") {
			name, arrays = strings.TrimSuffix(name, "[]"), arrays+1
		}
		var next []any
		for _, node := range current {
			obj, ok := node.(map[string]any)
			if !ok {
				continue
			}
			if child, ok := obj[name]; ok {
				next = append(next, child)
			}
		}
		for range arrays {
			var elems []any
			for _, node := range next {
				if arr, ok := node.([]any); ok {
					elems = append(elems, arr...)
				}
			}
			next = elems
		}
		current = next
	}
	return current
}

// scalarText renders a scalar the way an operator types it into an equals
// condition; objects and arrays have no text and never equal anything.
func scalarText(v any) (string, bool) {
	switch typed := v.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case bool:
		if typed {
			return "true", true
		}
		return "false", true
	case nil:
		return "null", true
	default:
		return "", false
	}
}
