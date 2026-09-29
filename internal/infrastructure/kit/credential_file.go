package kit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// Credential-file placeholders, the vocabulary the Kit spec's
// credentialFile.structure declares. A leaf may reference the token
// sentinels as a whole value or inside a larger string; ExpiresAt is a
// number and so may only be the whole value.
const (
	accessPlaceholder  = "{{.AccessToken}}"
	refreshPlaceholder = "{{.RefreshToken}}"
	expiresPlaceholder = "{{.ExpiresAt}}"

	// credentialFileMode is owner-only: the file holds sentinels, but the
	// path is still a credential the agent alone should read.
	credentialFileMode = "0600"
)

// renderCredentialFile renders c's oauth.credentialFile: the declared
// structure with the Kit's sentinels substituted for the token placeholders
// and the stored token set's expiry for {{.ExpiresAt}}, encoded in the
// declared format. expires is the only fact about the stored token set the
// renderer receives, so a real token cannot reach the file
// (docs/prds/external-agent-harness.md, "Verification: the credential file
// holds only sentinels").
func renderCredentialFile(c spec.CredentialCapability, expires time.Time) (string, error) {
	cf := c.OAuth.CredentialFile
	var sentinels spec.Sentinels
	if c.OAuth.Sentinels != nil {
		sentinels = *c.OAuth.Sentinels
	}
	expiresAt := int64(0)
	if !expires.IsZero() {
		// Both in-spec OAuth credential files (Claude Code's credentials.json
		// and OpenCode's auth.json) carry the epoch in milliseconds.
		expiresAt = expires.UnixMilli()
	}
	structure, err := substitutePlaceholders(cf.Structure, sentinels, expiresAt)
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
func substitutePlaceholders(v any, sentinels spec.Sentinels, expiresAt int64) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			sub, err := substitutePlaceholders(e, sentinels, expiresAt)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = sub
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			sub, err := substitutePlaceholders(e, sentinels, expiresAt)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = sub
		}
		return out, nil
	case string:
		return substituteString(t, sentinels, expiresAt)
	default:
		return v, nil
	}
}

func substituteString(s string, sentinels spec.Sentinels, expiresAt int64) (any, error) {
	switch s {
	case accessPlaceholder:
		if sentinels.AccessToken == "" {
			return nil, fmt.Errorf("no access-token sentinel is declared for this credential")
		}
		return sentinels.AccessToken, nil
	case refreshPlaceholder:
		if sentinels.RefreshToken == "" {
			return nil, fmt.Errorf("no refresh-token sentinel is declared for this credential")
		}
		return sentinels.RefreshToken, nil
	case expiresPlaceholder:
		return expiresAt, nil
	}
	// A token sentinel may ride inside a larger string, e.g. "Bearer ...".
	out := s
	if strings.Contains(out, accessPlaceholder) {
		if sentinels.AccessToken == "" {
			return nil, fmt.Errorf("no access-token sentinel is declared for this credential")
		}
		out = strings.ReplaceAll(out, accessPlaceholder, sentinels.AccessToken)
	}
	if strings.Contains(out, refreshPlaceholder) {
		if sentinels.RefreshToken == "" {
			return nil, fmt.Errorf("no refresh-token sentinel is declared for this credential")
		}
		out = strings.ReplaceAll(out, refreshPlaceholder, sentinels.RefreshToken)
	}
	if at := firstPlaceholder(out); at != "" {
		return nil, fmt.Errorf("placeholder %s is not rendered by archie", at)
	}
	return out, nil
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
