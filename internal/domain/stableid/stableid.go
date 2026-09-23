// Package stableid owns the stable-identifier shape shared by plugin
// manifests, workflow step types and playbook action ids: a lowercase,
// dotted or dashed identifier, so one name has exactly one spelling and
// cannot smuggle whitespace or case into a vocabulary two processes compare.
package stableid

import "regexp"

// Pattern is the stable-identifier grammar, for error messages that quote it.
const Pattern = `^[a-z][a-z0-9]*(?:[.-][a-z0-9]+)*$`

var re = regexp.MustCompile(Pattern)

// Valid reports whether id is a stable identifier.
func Valid(id string) bool { return re.MatchString(id) }
