package archiemessaging

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

type versionChatContract struct {
	messaging.ChatContract
	version string
	err     error
	calls   int
}

func (c *versionChatContract) Snapshot(context.Context) (messaging.ChatSnapshot, error) {
	c.calls++
	return messaging.ChatSnapshot{Version: c.version}, c.err
}

func telegramChannel(t *testing.T, srv *Service) *telegram.Gateway {
	t.Helper()
	for _, instance := range srv.channels {
		if instance.name == "telegram" {
			gateway, ok := instance.channel.(*telegram.Gateway)
			if !ok {
				t.Fatalf("telegram channel is %T, want *telegram.Gateway", instance.channel)
			}
			return gateway
		}
	}
	t.Fatal("no telegram channel was composed")
	return nil
}

func telegramConfig(t *testing.T, cfg ResolvedConfig, chat messaging.ChatContract) *telegram.Gateway {
	t.Helper()
	cfg.TelegramToken = "token-123"
	cfg.Telegram.TokenEnv = "TEST_TG_TOKEN"
	cfg.Telegram.AllowedUserIDs = []int64{7}
	srv, err := compose(t.Context(), deps{Config: cfg, Log: slog.New(slog.DiscardHandler), Chat: chat})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	return telegramChannel(t, srv)
}

// TestTelegramVersionComesFromTheGateway pins that /version renders the
// Gateway's own build block. The Messaging Service takes no version ldflags
// (see docs/architecture/migration-decisions.md), so anything it reported from
// its own process would be a guess that hides a partial upgrade.
func TestTelegramVersionComesFromTheGateway(t *testing.T) {
	chat := &versionChatContract{version: "Archie\nGateway: 1.2.3\nRuntime: 4.5.6"}
	gateway := telegramConfig(t, ResolvedConfig{}, chat)

	if gateway.Version == nil {
		t.Fatal("telegram Version = nil, want a version function")
	}
	if got := gateway.Version(); got != chat.version {
		t.Fatalf("Version() = %q, want %q", got, chat.version)
	}
	if chat.calls != 1 {
		t.Fatalf("Snapshot calls = %d, want 1: the version is read per call, not cached at compose", chat.calls)
	}
}

// TestTelegramVersionReportsAnUnreachableGateway pins that a Gateway that
// cannot be reached is said so rather than rendered as an empty version block,
// which reads as "no components installed".
func TestTelegramVersionReportsAnUnreachableGateway(t *testing.T) {
	chat := &versionChatContract{err: context.DeadlineExceeded}
	gateway := telegramConfig(t, ResolvedConfig{}, chat)

	got := gateway.Version()
	if got == "" || !strings.Contains(strings.ToLower(got), "unavailable") {
		t.Fatalf("Version() = %q, want an unavailable message", got)
	}
}

// TestTelegramRunningVersionsStaysUnwired pins the decision recorded in
// docs/architecture/migration-decisions.md: RunningVersions turns an
// installer's claim into a checked one, and only a component's own compiled-in
// build can vouch for itself. This process knows neither archied's build nor
// the observed archie-agent version, so reporting anything here would
// manufacture the false success the check exists to catch.
func TestTelegramRunningVersionsStaysUnwired(t *testing.T) {
	gateway := telegramConfig(t, ResolvedConfig{}, &versionChatContract{})

	if gateway.RunningVersions != nil {
		t.Fatal("telegram RunningVersions is wired; update reports must stay unverified claims")
	}
	if gateway.Dangerous != nil {
		t.Fatal("telegram Dangerous is wired; no composition has ever supplied a command authority")
	}
}

// TestTelegramUpdateServiceIsBuiltFromChannelConfig pins that /update survives
// the extraction: the check and install commands are [chat.telegram] config,
// which this service owns.
func TestTelegramUpdateServiceIsBuiltFromChannelConfig(t *testing.T) {
	workDir := t.TempDir()
	cfg := ResolvedConfig{
		WorkDir: workDir,
		Telegram: config.TelegramConfig{
			UpdateCheckCommand:   []string{"archie-update", "check"},
			UpdateInstallCommand: []string{"archie-update", "install"},
		},
	}
	gateway := telegramConfig(t, cfg, &versionChatContract{})

	if gateway.Updates == nil {
		t.Fatal("telegram Updates = nil, want the update service")
	}
	if !gateway.Updates.CanInstall() {
		t.Fatal("Updates.CanInstall() = false, want true: an install command is configured")
	}
}

// TestTelegramUpdateServiceOmittedWithoutACheckCommand pins that an operator
// who configured no update command gets the gateway's "not configured" reply
// rather than a service that shells out to nothing.
func TestTelegramUpdateServiceOmittedWithoutACheckCommand(t *testing.T) {
	gateway := telegramConfig(t, ResolvedConfig{WorkDir: t.TempDir()}, &versionChatContract{})

	if gateway.Updates != nil {
		t.Fatalf("telegram Updates = %T, want nil without an update_check_command", gateway.Updates)
	}
}

// TestTelegramStatePathsKeepTheDaemonHashScheme pins that the update-report
// file is the same one the daemon wrote before the extraction. A different path
// silently stops relaying the watchdog's verdict on the last update.
//
// ReleaseAnnouncements stays nil: see the decision table in
// docs/architecture/migration-decisions.md.
func TestTelegramStatePathsKeepTheDaemonHashScheme(t *testing.T) {
	workDir := t.TempDir()
	cfg := ResolvedConfig{WorkDir: workDir, BotUser: "archie-bot"}
	gateway := telegramConfig(t, cfg, &versionChatContract{})

	// sha256("archie-bot")[:8], the scheme internal/app/archied used.
	const wantSuffix = "update-report-95db552fa3a1e0be.json"
	if got := gateway.UpdateReportPath; got != filepath.Join(workDir, wantSuffix) {
		t.Fatalf("UpdateReportPath = %q, want %q", got, filepath.Join(workDir, wantSuffix))
	}
	if gateway.ReleaseAnnouncements != nil {
		t.Fatal("telegram ReleaseAnnouncements is wired; without trustworthy component versions the announcer is a no-op that reads as configured")
	}
}

// TestTelegramShowToolCallsIsProjected pins that [chat].show_tool_calls reaches
// the channel; it is read per reply, so an unset projection silently turns tool
// visibility off for every deployment that asked for it.
func TestTelegramShowToolCallsIsProjected(t *testing.T) {
	gateway := telegramConfig(t, ResolvedConfig{ShowToolCalls: true}, &versionChatContract{})

	if !gateway.ShowToolCalls() {
		t.Fatal("ShowToolCalls() = false, want true")
	}
}

// TestTelegramReloadRereadsTokenAndAllowlist pins that /restart picks up a
// changed allowlist from this service's own config file. Reload was never a
// daemon fact: the file it re-reads is the one this process was started with.
func TestTelegramReloadRereadsTokenAndAllowlist(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	write := func(userIDs string) {
		content := "bot_user = \"testbot\"\n\n[chat.telegram]\ntoken_env = \"TEST_TG_TOKEN\"\nallowed_user_ids = " + userIDs + "\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[1]")
	t.Setenv("TEST_TG_TOKEN", "first-token")

	resolved, err := Resolve(Options{Config: cfgPath}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	srv, err := compose(t.Context(), deps{Config: resolved, Log: slog.New(slog.DiscardHandler), Chat: &versionChatContract{}})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	gateway := telegramChannel(t, srv)
	if gateway.Reload == nil {
		t.Fatal("telegram Reload = nil, want a reload function")
	}

	write("[1, 2]")
	t.Setenv("TEST_TG_TOKEN", "second-token")
	if err := gateway.Reload(gateway); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(gateway.AllowedUserIDs) != 2 {
		t.Fatalf("AllowedUserIDs = %v, want two entries after reload", gateway.AllowedUserIDs)
	}
	if gateway.Token != "second-token" {
		t.Fatalf("Token = %q, want second-token", gateway.Token)
	}
}

// TestTelegramReloadRefusesAnEmptyToken pins that a config edit which loses the
// credential leaves the running bot on its working token rather than silently
// authenticating as nobody.
func TestTelegramReloadRefusesAnEmptyToken(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	content := "bot_user = \"testbot\"\n\n[chat.telegram]\ntoken_env = \"TEST_TG_TOKEN\"\nallowed_user_ids = [1]\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TG_TOKEN", "first-token")

	resolved, err := Resolve(Options{Config: cfgPath}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	srv, err := compose(t.Context(), deps{Config: resolved, Log: slog.New(slog.DiscardHandler), Chat: &versionChatContract{}})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	gateway := telegramChannel(t, srv)

	t.Setenv("TEST_TG_TOKEN", "")
	if err := gateway.Reload(gateway); err == nil {
		t.Fatal("Reload error = nil, want an error when the token resolves empty")
	}
	if gateway.Token != "first-token" {
		t.Fatalf("Token = %q, want the pre-reload token to survive a refused reload", gateway.Token)
	}
}
