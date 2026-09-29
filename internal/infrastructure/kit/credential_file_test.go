package kit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/docker/sandbox-kit-spec/v3/spec"
)

// oauthCredentialFileKit is a Kit declaring a proxy-managed OAuth credential
// whose harness reads its tokens from a credential file, as the published
// Claude Code Kit does (the spec's own example).
const oauthCredentialFileKit = sessions + `
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
              expiresAt: "{{.ExpiresAt}}"`

func credentialFilePlan(t *testing.T) *Plan {
	t.Helper()
	p, err := Admit(descriptor(t, spec.KindWorkload, oauthCredentialFileKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	return p
}

func findFile(files []spec.File, path string) (spec.File, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return spec.File{}, false
}

// TestAssembleRendersTheOAuthCredentialFile is the sentinel rule: a bound
// OAuth credential whose Kit declares a credentialFile gets that file written
// at the declared path, holding the Kit's sentinels and never a real token.
func TestAssembleRendersTheOAuthCredentialFile(t *testing.T) {
	l, err := Assemble(credentialFilePlan(t), image, LaunchParams{
		Execution: "exec-42", ProxyToken: "tok", CAPath: "/etc/archie/ca.pem",
		Bound: []string{"claude-code"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(l.Files, "/home/agent/.claude/.credentials.json")
	if !ok {
		t.Fatalf("no credential file at the declared path; files = %v", l.Files)
	}
	if f.Mode != "0600" {
		t.Errorf("credential file mode = %q, want 0600", f.Mode)
	}
	if strings.Contains(f.Content, "{{") {
		t.Errorf("credential file still holds a placeholder: %s", f.Content)
	}
	var doc struct {
		ClaudeAiOauth struct {
			AccessToken  string  `json:"accessToken"`
			RefreshToken string  `json:"refreshToken"`
			ExpiresAt    float64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
		t.Fatalf("credential file is not JSON: %v\n%s", err, f.Content)
	}
	if doc.ClaudeAiOauth.AccessToken != "sk-ant-oat01-proxy-managed" {
		t.Errorf("accessToken = %q, want the Kit's access-token sentinel", doc.ClaudeAiOauth.AccessToken)
	}
	if doc.ClaudeAiOauth.RefreshToken != "sk-ant-ort01-proxy-managed" {
		t.Errorf("refreshToken = %q, want the Kit's refresh-token sentinel", doc.ClaudeAiOauth.RefreshToken)
	}
	if doc.ClaudeAiOauth.ExpiresAt != 0 {
		t.Errorf("expiresAt = %v, want 0 with no stored token set", doc.ClaudeAiOauth.ExpiresAt)
	}
}

// TestAssembleRendersTheStoredExpiry proves the credential file carries the
// stored token set's expiry -- the value the CLI checks before it decides to
// refresh -- and that it is a number, not a quoted placeholder.
func TestAssembleRendersTheStoredExpiry(t *testing.T) {
	expires := time.UnixMilli(1700000000123)
	l, err := Assemble(credentialFilePlan(t), image, LaunchParams{
		Execution: "exec-42", ProxyToken: "tok", CAPath: "/etc/archie/ca.pem",
		Bound: []string{"claude-code"}, OAuth: map[string]OAuthFacts{"claude-code": {ExpiresAt: expires}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(l.Files, "/home/agent/.claude/.credentials.json")
	if !ok {
		t.Fatalf("no credential file at the declared path; files = %v", l.Files)
	}
	var doc struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal([]byte(f.Content), &doc); err != nil {
		t.Fatalf("credential file is not JSON: %v\n%s", err, f.Content)
	}
	if doc.ClaudeAiOauth.ExpiresAt != expires.UnixMilli() {
		t.Errorf("expiresAt = %d, want the stored expiry %d", doc.ClaudeAiOauth.ExpiresAt, expires.UnixMilli())
	}
}

// TestAssembleRendersATOMLCredentialFile covers the non-default encoding the
// spec provides for agents that read TOML credentials files.
func TestAssembleRendersATOMLCredentialFile(t *testing.T) {
	const tomlKit = sessions + `
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      oauth:
        tokenEndpoint: {host: auth.example.com}
        sentinels: {accessToken: sentinel-access, refreshToken: sentinel-refresh}
        credentialFile:
          path: /home/agent/credentials.toml
          format: toml
          structure:
            api_url: "https://api.example.com"
            tokens:
              access_token: "{{.AccessToken}}"
              refresh_token: "{{.RefreshToken}}"`
	p, err := Admit(descriptor(t, spec.KindWorkload, tomlKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	l, err := Assemble(p, image, LaunchParams{Execution: "exec-42", Bound: []string{"example"}})
	if err != nil {
		t.Fatal(err)
	}
	f, ok := findFile(l.Files, "/home/agent/credentials.toml")
	if !ok {
		t.Fatalf("no credential file at the declared path; files = %v", l.Files)
	}
	var doc struct {
		APIURL string `toml:"api_url"`
		Tokens struct {
			AccessToken  string `toml:"access_token"`
			RefreshToken string `toml:"refresh_token"`
		} `toml:"tokens"`
	}
	if _, err := toml.Decode(f.Content, &doc); err != nil {
		t.Fatalf("credential file is not TOML: %v\n%s", err, f.Content)
	}
	if doc.Tokens.AccessToken != "sentinel-access" || doc.Tokens.RefreshToken != "sentinel-refresh" {
		t.Errorf("tokens = %+v, want the sentinels", doc.Tokens)
	}
}

// TestAssembleRefusesAPlaceholderItCannotRender: a placeholder archie does
// not supply must refuse the launch rather than leave the literal standing in
// the file, which the CLI would read as a credential.
func TestAssembleRefusesAPlaceholderItCannotRender(t *testing.T) {
	const unknownKit = sessions + `
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      oauth:
        tokenEndpoint: {host: auth.example.com}
        sentinels: {accessToken: sentinel-access}
        credentialFile:
          path: /home/agent/credentials.json
          structure:
            mystery: "{{.NotAPlaceholder}}"`
	p, err := Admit(descriptor(t, spec.KindWorkload, unknownKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	_, err = Assemble(p, image, LaunchParams{Execution: "exec-42", Bound: []string{"example"}})
	if err == nil || !strings.Contains(err.Error(), "{{.NotAPlaceholder}}") {
		t.Fatalf("Assemble = %v, want a refusal naming {{.NotAPlaceholder}}", err)
	}
}

// TestAssembleRefusesAMissingSentinel: a token placeholder with no declared
// sentinel cannot be hidden, so the file must not be written at all.
func TestAssembleRefusesAMissingSentinel(t *testing.T) {
	const noSentinelKit = sessions + `
  - type: com.docker.sandbox/credential@1
    config:
      service: example
      phase: runtime
      oauth:
        tokenEndpoint: {host: auth.example.com}
        credentialFile:
          path: /home/agent/credentials.json
          structure:
            access_token: "{{.AccessToken}}"`
	p, err := Admit(descriptor(t, spec.KindWorkload, noSentinelKit))
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	_, err = Assemble(p, image, LaunchParams{Execution: "exec-42", Bound: []string{"example"}})
	if err == nil || !strings.Contains(err.Error(), "access-token sentinel") {
		t.Fatalf("Assemble = %v, want a refusal naming the missing access-token sentinel", err)
	}
}
