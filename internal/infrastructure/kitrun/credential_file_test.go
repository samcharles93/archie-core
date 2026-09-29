package kitrun

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/docker/sandbox-kit-spec/v3/spec"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/infrastructure/kit"
)

type fakeOAuthSecrets struct {
	secret harnesssecret.Secret
	err    error
}

func (f fakeOAuthSecrets) GetHarnessSecret(context.Context, string, string) (harnesssecret.Secret, error) {
	if f.err != nil {
		return harnesssecret.Secret{}, f.err
	}
	return f.secret, nil
}

const credentialFileKit = `
schemaVersion: "3"
kind: workload
capabilities:
  - type: com.docker.sandbox/agent-sessions@1
    config: {prompt: [-p, "{{.Prompt}}"]}
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
`

func credentialFilePlan(t *testing.T) *kit.Plan {
	t.Helper()
	d, err := spec.Decode([]byte(credentialFileKit))
	if err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	p, err := kit.Admit(d)
	if err != nil {
		t.Fatalf("admit: %v", err)
	}
	return p
}

func planCredentials(t *testing.T, p *kit.Plan) []spec.CredentialCapability {
	t.Helper()
	creds, err := spec.CredentialsOf(p.Capabilities)
	if err != nil {
		t.Fatalf("credentials of plan: %v", err)
	}
	return creds
}

// TestCredentialFileNeverCarriesARealToken is the sentinel rule end to end:
// the store holds real tokens, kitrun reads the set only for its expiry, and
// the assembled credential file holds the Kit's sentinels instead. The store's
// real values must not appear anywhere in the rendered file.
func TestCredentialFileNeverCarriesARealToken(t *testing.T) {
	expires := time.UnixMilli(1700000000123)
	ancestors := []string{"user:inference", "user:profile"}
	l := &Launcher{OAuth: fakeOAuthSecrets{secret: harnesssecret.Secret{
		Org: "acme", Service: "claude-code",
		AccessToken: "real-access-token", RefreshToken: "real-refresh-token",
		ExpiresAt: expires, Scopes: ancestors,
	}}}
	p := credentialFilePlan(t)
	bound := []string{"claude-code"}

	facts, err := l.oauthFacts(context.Background(), "acme", planCredentials(t, p), bound)
	if err != nil {
		t.Fatal(err)
	}
	if got := facts["claude-code"].ExpiresAt; got != expires {
		t.Fatalf("oauthFacts[claude-code].ExpiresAt = %v, want the stored expiry %v", got, expires)
	}
	if got := facts["claude-code"].Scopes; !slices.Equal(got, ancestors) {
		t.Fatalf("oauthFacts[claude-code].Scopes = %v, want the stored scopes %v", got, ancestors)
	}

	launch, err := kit.Assemble(p, kit.ImageConfig{User: "agent"}, kit.LaunchParams{
		Execution: "exec-1", Bound: bound, OAuth: facts,
	})
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for _, f := range launch.Files {
		if f.Path == "/home/agent/.claude/.credentials.json" {
			content = f.Content
		}
	}
	if content == "" {
		t.Fatalf("no credential file rendered; files = %v", launch.Files)
	}
	if strings.Contains(content, "real-access-token") || strings.Contains(content, "real-refresh-token") {
		t.Fatalf("credential file carries a real token: %s", content)
	}
	if !strings.Contains(content, "sk-ant-oat01-proxy-managed") || !strings.Contains(content, "sk-ant-ort01-proxy-managed") {
		t.Fatalf("credential file does not hold the sentinels: %s", content)
	}
}

// TestOAuthExpiriesFailsClosed: a Kit that renders a credential file cannot be
// launched without the stored token set its expiry comes from.
func TestOAuthExpiriesFailsClosed(t *testing.T) {
	p := credentialFilePlan(t)
	creds := planCredentials(t, p)
	bound := []string{"claude-code"}
	ctx := context.Background()

	if _, err := (&Launcher{}).oauthFacts(ctx, "acme", creds, bound); err == nil {
		t.Fatal("oauthFacts with no store = nil error, want a refusal")
	}
	l := &Launcher{OAuth: fakeOAuthSecrets{err: errors.New("no captured token")}}
	if _, err := l.oauthFacts(ctx, "acme", creds, bound); err == nil {
		t.Fatal("oauthFacts with no stored token = nil error, want a refusal")
	}
}
