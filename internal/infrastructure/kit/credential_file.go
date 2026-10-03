package kit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// Credential-file placeholders, the vocabulary the Kit spec's
// credentialFile.structure declares. A leaf may reference the token
// sentinels as a whole value or inside a larger string; ExpiresAt (a number)
// and Scopes (an array) may only be the whole value, and PrimaryApiKey's
// enclosing key is dropped rather than substituted.
const (
	accessPlaceholder  = "{{.AccessToken}}"
	refreshPlaceholder = "{{.RefreshToken}}"
	expiresPlaceholder = "{{.ExpiresAt}}"
	scopesPlaceholder  = "{{.Scopes}}"
	// primaryKeyPlaceholder is the spec's omit marker, not a value to render:
	// see substitutePlaceholders.
	primaryKeyPlaceholder = "{{.PrimaryApiKey}}"

	// credentialFileMode is owner-only: the file holds sentinels, but the
	// path is still a credential the agent alone should read.
	credentialFileMode = "0600"
)

// renderCredentialFile renders c's oauth.credentialFile: the declared structure
// with the Kit's sentinels substituted for the token placeholders, the stored
// token set's scopes for {{.Scopes}} and its expiry for {{.ExpiresAt}}, encoded
// in the declared format. facts is the only thing the renderer receives about
// the stored token set, so a real token cannot reach the file.
func renderCredentialFile(c spec.CredentialCapability, facts OAuthFacts) (string, error) {
	cf := c.OAuth.CredentialFile
	var sentinels spec.Sentinels
	if c.OAuth.Sentinels != nil {
		sentinels = *c.OAuth.Sentinels
	}
	expiresAt := int64(0)
	if !facts.ExpiresAt.IsZero() {
		// Both in-spec OAuth credential files (Claude Code's credentials.json
		// and OpenCode's auth.json) carry the epoch in milliseconds.
		expiresAt = facts.ExpiresAt.UnixMilli()
	}
	structure, err := substitutePlaceholders(cf.Structure, sentinels, facts.Scopes, expiresAt)
	if err != nil {
		return "", fmt.Errorf("credential %q credential file: %w", c.Service, err)
	}
	encoded, err := encodeCredentialFile(cf.Format, structure)
	if err != nil {
		return "", fmt.Errorf("credential %q credential file: %w", c.Service, err)
	}
	return encoded, nil
}

// substitutePlaceholders replaces every leaf that references a placeholder
// with the typed value the target encoding expects, and refuses a placeholder
// archie does not render rather than leaving it standing in the file.
func substitutePlaceholders(v any, sentinels spec.Sentinels, scopes []string, expiresAt int64) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			// {{.PrimaryApiKey}}'s spec case is that its ENCLOSING KEY is omitted
			// when no key is captured. archie resolves a service key only to fill
			// the egress grant and the container holds egress.Sentinel, so there
			// is never a key to render here: drop the key, never empty it (absent
			// and empty are different bugs). Do not "fix" this into a value.
			if s, ok := e.(string); ok && s == primaryKeyPlaceholder {
				continue
			}
			sub, err := substitutePlaceholders(e, sentinels, scopes, expiresAt)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = sub
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			sub, err := substitutePlaceholders(e, sentinels, scopes, expiresAt)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = sub
		}
		return out, nil
	case string:
		return substituteString(t, sentinels, scopes, expiresAt)
	default:
		return v, nil
	}
}

func substituteString(s string, sentinels spec.Sentinels, scopes []string, expiresAt int64) (any, error) {
	switch s {
	case accessPlaceholder:
		return tokenSentinel(sentinels.AccessToken, "access-token")
	case refreshPlaceholder:
		return tokenSentinel(sentinels.RefreshToken, "refresh-token")
	case expiresPlaceholder:
		return expiresAt, nil
	case scopesPlaceholder:
		return grantedScopes(scopes)
	}
	out, err := spliceSentinels(s, sentinels)
	if err != nil {
		return nil, err
	}
	if err := valuePlaceholderInString(out); err != nil {
		return nil, err
	}
	if at := firstPlaceholder(out); at != "" {
		return nil, fmt.Errorf("placeholder %s is not rendered by archie", at)
	}
	return out, nil
}

// tokenSentinel returns the sentinel standing in for a token, or an error when
// the Kit declared none: a token placeholder that cannot be hidden must not
// reach the file.
func tokenSentinel(sentinel, name string) (string, error) {
	if sentinel == "" {
		return "", fmt.Errorf("no %s sentinel is declared for this credential", name)
	}
	return sentinel, nil
}

// grantedScopes renders {{.Scopes}} as the array the target encoding expects.
// The spec declares no omission case for it the way it does for
// {{.PrimaryApiKey}}, so an empty set refuses rather than writing an empty
// array, which would silently change the CLI's capability decisions.
func grantedScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%s: no scopes were captured for this credential; re-run the setup terminal so the provider's granted scopes are recorded", scopesPlaceholder)
	}
	return scopes, nil
}

// spliceSentinels replaces a token placeholder that rides inside a larger
// string, e.g. "Bearer {{.AccessToken}}".
func spliceSentinels(s string, sentinels spec.Sentinels) (string, error) {
	for _, tok := range []struct{ placeholder, sentinel, name string }{
		{accessPlaceholder, sentinels.AccessToken, "access-token"},
		{refreshPlaceholder, sentinels.RefreshToken, "refresh-token"},
	} {
		if !strings.Contains(s, tok.placeholder) {
			continue
		}
		sentinel, err := tokenSentinel(tok.sentinel, tok.name)
		if err != nil {
			return "", err
		}
		s = strings.ReplaceAll(s, tok.placeholder, sentinel)
	}
	return s, nil
}

// valuePlaceholderInString rejects a value placeholder left inside a larger
// string: a number or an array renders in the encoding's own type and cannot
// ride inside a string, and the primary key is omitted rather than rendered.
func valuePlaceholderInString(s string) error {
	for _, whole := range []string{expiresPlaceholder, scopesPlaceholder} {
		if strings.Contains(s, whole) {
			return fmt.Errorf("%s must be a field's whole value", whole)
		}
	}
	if strings.Contains(s, primaryKeyPlaceholder) {
		return fmt.Errorf("%s is omitted, never rendered, so it cannot appear inside a larger string", primaryKeyPlaceholder)
	}
	return nil
}

// firstPlaceholder reports the first {{...}} occurrence in s, or "".
func firstPlaceholder(s string) string {
	start := strings.Index(s, "{{")
	if start < 0 {
		return ""
	}
	end := strings.Index(s[start:], "}}")
	if end < 0 {
		return s[start:]
	}
	return s[start : start+end+2]
}

// encodeCredentialFile writes the substituted structure in the Kit's declared
// format: json unless it names toml.
func encodeCredentialFile(format string, structure any) (string, error) {
	switch format {
	case "", "json":
		b, err := json.Marshal(structure)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case "toml":
		var buf strings.Builder
		if err := toml.NewEncoder(&buf).Encode(structure); err != nil {
			return "", err
		}
		return buf.String(), nil
	default:
		return "", fmt.Errorf("unknown format %q", format)
	}
}
