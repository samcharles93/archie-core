package setup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
)

// engineNameRe is the shape a secret engine name must have. Engine names are
// identifiers -- "env", "bws", "builtin" -- and for operator-supplied engines
// they are also Yaegi plugin filenames, so this is deliberately no tighter than
// an identifier.
var engineNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ParseSecretRef parses an "engine:key" secret reference.
//
// A reference is not a secret. An engine name and a key name are not sensitive,
// which is exactly why a reference may be given as a parameter while a value may
// not: the value lives in the engine, and this flow never sees it. Setup writes
// the reference to TOML and stops there.
func ParseSecretRef(s string) (config.SecretRef, error) {
	engine, key, ok := strings.Cut(s, ":")
	if !ok {
		return config.SecretRef{}, fmt.Errorf("secret reference %q must be engine:key, e.g. env:ARCHIE_GITHUB_TOKEN", s)
	}
	engine, key = strings.TrimSpace(engine), strings.TrimSpace(key)
	if engine == "" || key == "" {
		return config.SecretRef{}, fmt.Errorf("secret reference %q must name both an engine and a key, e.g. env:ARCHIE_GITHUB_TOKEN", s)
	}
	if !engineNameRe.MatchString(engine) {
		return config.SecretRef{}, fmt.Errorf("secret reference engine %q is not a valid engine name", engine)
	}
	if strings.ContainsAny(key, ": \t\n") {
		return config.SecretRef{}, fmt.Errorf("secret reference key %q must not contain whitespace or a colon", key)
	}
	return config.SecretRef{Engine: engine, Key: key}, nil
}
