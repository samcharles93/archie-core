package kit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// claudeCodeCredentialFileKit mirrors the published Claude Code Kit's
// credentialFile: the OAuth fields nested, the granted scopes among them, and
// a top-level primaryApiKey.
const claudeCodeCredentialFileKit = sessions + `
  - type: com.docker.sandbox/credential@1
    config:
      service: claude-code
      phase: runtime
      oauth:
        tokenEndpoint: {host: platform.claude.com, path: /v1/oauth/token}
        sentinels:
          accessToken: sk-ant-oat01-proxy-managed
          refreshToken: sk-ant-ort01-proxy-managed
        credentialFile:
          path: /home/agent/.claude/.credentials.json
          structure:
            claudeAiOauth:
              accessToken: "{{.AccessToken}}"
              refreshToken: "{{.RefreshToken}}"
              expiresAt: "{{.ExpiresAt}}"
              scopes: "{{.Scopes}}"
            primaryApiKey: "{{.PrimaryApiKey}}"`

// renderClaudeCredentialFile assembles the Claude-shaped Kit with facts and
// returns the credential file's decoded top level.
func renderClaudeCredentialFile(t *testing.T, facts OAuthFacts) map[string]any {
	t.Helper()
	p, err := Admit(descriptor(t, spec.KindWorkload, claudeCodeCredentialFileKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	l, err := Assemble(p, image, LaunchParams{
		Execution: "exec-42", Bound: map[string]egress.CredentialKind{"claude-code": egress.CredentialOAuth},
		OAuth: map[string]OAuthFacts{"claude-code": facts},
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	f, ok := findFile(l.Files, "/home/agent/.claude/.credentials.json")
	if !ok {
		t.Fatalf("no credential file at the declared path; files = %v", l.Files)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
		t.Fatalf("credential file is not JSON: %v\n%s", err, f.Content)
	}
	return doc
}

// TestAssembleRendersTheGrantedScopes: {{.Scopes}} is an array in the target
// encoding, so a one-element set must still be a one-element ARRAY and never
// collapse to a bare string -- the single and multi cases differ exactly
// there, and nowhere else.
func TestAssembleRendersTheGrantedScopes(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
	}{
		{name: "a single granted scope is a one-element array", scopes: []string{"user:inference"}},
		{name: "several granted scopes are a multi-element array", scopes: []string{"user:inference", "user:profile"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := renderClaudeCredentialFile(t, OAuthFacts{Scopes: tt.scopes})
			oauth, _ := doc["claudeAiOauth"].(map[string]any)
			if oauth == nil {
				t.Fatalf("claudeAiOauth = %v, want an object", doc["claudeAiOauth"])
			}
			raw, ok := oauth["scopes"].([]any)
			if !ok {
				t.Fatalf("scopes = %#v (%T), want a JSON array", oauth["scopes"], oauth["scopes"])
			}
			if len(raw) != len(tt.scopes) {
				t.Fatalf("scopes = %v, want %v", raw, tt.scopes)
			}
			for i, want := range tt.scopes {
				if raw[i] != want {
					t.Fatalf("scopes[%d] = %v, want %q", i, raw[i], want)
				}
			}
		})
	}
}

// TestAssembleRefusesWhenNoScopesWereCaptured: the spec declares no omission
// case for {{.Scopes}}, so a token set with no scopes refuses the launch with
// something the operator can act on rather than writing an empty array, which
// would silently change the CLI's capability decisions.
func TestAssembleRefusesWhenNoScopesWereCaptured(t *testing.T) {
	p, err := Admit(descriptor(t, spec.KindWorkload, claudeCodeCredentialFileKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	_, err = Assemble(p, image, LaunchParams{
		Execution: "exec-42", Bound: map[string]egress.CredentialKind{"claude-code": egress.CredentialOAuth},
		OAuth: map[string]OAuthFacts{"claude-code": {}},
	})
	if err == nil {
		t.Fatal("Assemble with no captured scopes = nil error, want a refusal")
	}
	for _, want := range []string{"{{.Scopes}}", "setup terminal"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Assemble error = %q, want it to name %q", err, want)
		}
	}
}

// TestAssembleOmitsAnUncapturedPrimaryApiKey: the spec's case for
// {{.PrimaryApiKey}} is that its enclosing key is omitted when no key is
// captured. archie never hands the container a key -- it resolves one only to
// fill the egress grant -- so the key must be ABSENT; absent and empty are
// different bugs.
func TestAssembleOmitsAnUncapturedPrimaryApiKey(t *testing.T) {
	doc := renderClaudeCredentialFile(t, OAuthFacts{Scopes: []string{"user:inference"}})
	if v, present := doc["primaryApiKey"]; present {
		t.Fatalf("primaryApiKey = %#v, want the key omitted entirely", v)
	}
	if _, ok := doc["claudeAiOauth"]; !ok {
		t.Fatalf("claudeAiOauth missing alongside primaryApiKey: %v", doc)
	}
}
