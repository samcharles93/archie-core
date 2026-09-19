package archiemessaging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOptionsDefaultsAndValidation(t *testing.T) {
	opts := withDefaults(Options{})
	if opts.Gateway.Target != defaultGatewayTarget {
		t.Errorf("Gateway.Target = %q, want %q", opts.Gateway.Target, defaultGatewayTarget)
	}
	if opts.DependencyTimeout != defaultDependencyTimeout {
		t.Errorf("DependencyTimeout = %v, want %v", opts.DependencyTimeout, defaultDependencyTimeout)
	}
	if err := opts.validate(); err != nil {
		t.Errorf("validate default loopback options: %v", err)
	}
}

func TestOptionsNonLoopbackRequiresToken(t *testing.T) {
	opts := Options{
		Gateway: ServiceTarget{Target: "192.168.1.50:8585"},
	}
	opts = withDefaults(opts)
	if err := opts.validate(); err == nil {
		t.Error("validate non-loopback gateway target with empty token want error, got nil")
	}

	opts.Gateway.Token = "secret"
	if err := opts.validate(); err != nil {
		t.Errorf("validate non-loopback gateway target with token: %v", err)
	}
}

func TestResolveWithConfigFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	content := `
bot_user = "testbot"

[services.gateway]
target = "127.0.0.1:8999"
target_token = "tok-123"

[chat.telegram]
token_env = "TEST_TG_TOKEN"
allowed_user_ids = [12345]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TG_TOKEN", "resolved-tg-token")

	resolved, err := Resolve(Options{Config: cfgPath}, slog.Default())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.Options.Gateway.Target != "127.0.0.1:8999" {
		t.Errorf("Gateway.Target = %q, want 127.0.0.1:8999", resolved.Options.Gateway.Target)
	}
	if resolved.TelegramToken != "resolved-tg-token" {
		t.Errorf("TelegramToken = %q, want resolved-tg-token", resolved.TelegramToken)
	}
	if len(resolved.Telegram.AllowedUserIDs) != 1 || resolved.Telegram.AllowedUserIDs[0] != 12345 {
		t.Errorf("AllowedUserIDs = %v, want [12345]", resolved.Telegram.AllowedUserIDs)
	}
}

func TestServiceStartAndStop(t *testing.T) {
	d := deps{
		Config: ResolvedConfig{
			Options: withDefaults(Options{}),
		},
		Log: slog.Default(),
	}
	srv := compose(d)

	ctx, cancel := contextWithTimeout(t, 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("srv.Start: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("srv.Start timed out waiting for context cancel")
	}

	srv.Stop()
}

func contextWithTimeout(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), d)
}
