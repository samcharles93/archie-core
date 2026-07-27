package releaseannounce

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testChangelog = `# Changelog

## [Unreleased]

- feat: work in progress

## [v0.2.0] - 2026-07-27

- feat: improved the /help command
- fix: model selection now affects live chat

## [v0.1.0] - 2026-07-20

- feat: initial release
`

func TestAnnouncerBaselinesFirstReleaseWithoutSending(t *testing.T) {
	announcer := newTestAnnouncer(t, "v0.1.0")
	var sent []string

	if err := announcer.Announce(context.Background(), []int64{7}, func(_ context.Context, _ int64, message string) error {
		sent = append(sent, message)
		return nil
	}); err != nil {
		t.Fatalf("Announce() error = %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("first release sent %d messages, want baseline only", len(sent))
	}

	announcer.Components[0].Version = "v0.2.0"
	if err := announcer.Announce(context.Background(), []int64{7}, func(_ context.Context, _ int64, message string) error {
		sent = append(sent, message)
		return nil
	}); err != nil {
		t.Fatalf("Announce(upgrade) error = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("upgrade sent %d messages, want 1", len(sent))
	}
	for _, want := range []string{
		"Archie has just been updated.",
		"--- THE GATEWAY ---",
		"v0.2.0 installed - changes:",
		"- feat: improved the /help command",
		"- fix: model selection now affects live chat",
	} {
		if !strings.Contains(sent[0], want) {
			t.Errorf("notification missing %q:\n%s", want, sent[0])
		}
	}

	if err := announcer.Announce(context.Background(), []int64{7}, func(context.Context, int64, string) error {
		t.Fatal("same version was announced twice")
		return nil
	}); err != nil {
		t.Fatalf("Announce(restart) error = %v", err)
	}
}

func TestAnnouncerSeparatesChangedAndUnchangedComponents(t *testing.T) {
	dir := t.TempDir()
	gatewayChangelog := filepath.Join(dir, "CHANGELOG.archied.md")
	runtimeChangelog := filepath.Join(dir, "CHANGELOG.archie.md")
	if err := os.WriteFile(gatewayChangelog, []byte(testChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeChangelog, []byte(testChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	announcer := Announcer{
		StatePath: filepath.Join(dir, "announced.json"),
		Components: []Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: "v0.1.0", ChangelogPath: gatewayChangelog},
			{ID: "runtime", Label: "THE RUNTIME", Version: "v0.1.0", ChangelogPath: runtimeChangelog},
		},
	}
	if err := announcer.Announce(context.Background(), []int64{7}, func(context.Context, int64, string) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	announcer.Components[0].Version = "v0.2.0"
	var message string
	if err := announcer.Announce(context.Background(), []int64{7}, func(_ context.Context, _ int64, text string) error {
		message = text
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--- THE GATEWAY ---",
		"v0.2.0 installed - changes:",
		"- feat: improved the /help command",
		"--- THE RUNTIME ---",
		"No runtime changes as part of this release.",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("component notification missing %q:\n%s", want, message)
		}
	}
}

func TestAnnouncerTracksRecipientsIndependentlyAndRetriesFailures(t *testing.T) {
	announcer := newTestAnnouncer(t, "v0.1.0")
	if err := announcer.Announce(context.Background(), []int64{7, 8}, func(context.Context, int64, string) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	announcer.Components[0].Version = "v0.2.0"

	var attempts []int64
	send := func(_ context.Context, recipient int64, _ string) error {
		attempts = append(attempts, recipient)
		if recipient == 8 {
			return errors.New("telegram unavailable")
		}
		return nil
	}
	if err := announcer.Announce(context.Background(), []int64{7, 8}, send); err == nil {
		t.Fatal("Announce() error = nil, want delivery error")
	}

	attempts = nil
	if err := announcer.Announce(context.Background(), []int64{7, 8}, func(_ context.Context, recipient int64, _ string) error {
		attempts = append(attempts, recipient)
		return nil
	}); err != nil {
		t.Fatalf("Announce(retry) error = %v", err)
	}
	if len(attempts) != 1 || attempts[0] != 8 {
		t.Fatalf("retry recipients = %v, want only failed recipient 8", attempts)
	}
}

func TestAnnouncerSkipsDevelopmentBuildsAndVersionRollbacks(t *testing.T) {
	announcer := newTestAnnouncer(t, "dev")
	calls := 0
	send := func(context.Context, int64, string) error {
		calls++
		return nil
	}
	if err := announcer.Announce(context.Background(), []int64{7}, send); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(announcer.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("development build created state: %v", err)
	}

	announcer.Components[0].Version = "v0.2.0"
	if err := announcer.Announce(context.Background(), []int64{7}, send); err != nil {
		t.Fatal(err)
	}
	announcer.Components[0].Version = "v0.1.0"
	if err := announcer.Announce(context.Background(), []int64{7}, send); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("development/rollback sends = %d, want 0", calls)
	}
}

func TestAnnouncerReportsMissingReleaseSectionWithoutAdvancingState(t *testing.T) {
	announcer := newTestAnnouncer(t, "v0.1.0")
	if err := announcer.Announce(context.Background(), []int64{7}, func(context.Context, int64, string) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	announcer.Components[0].Version = "v9.9.9"

	if err := announcer.Announce(context.Background(), []int64{7}, func(context.Context, int64, string) error {
		t.Fatal("missing changelog section must not send")
		return nil
	}); err == nil {
		t.Fatal("Announce() error = nil, want missing release section error")
	}
}

func TestAnnouncerReportsStatePersistenceFailure(t *testing.T) {
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte(testChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	notDirectory := filepath.Join(dir, "file")
	if err := os.WriteFile(notDirectory, []byte("occupied"), 0o600); err != nil {
		t.Fatal(err)
	}
	announcer := Announcer{
		StatePath: filepath.Join(notDirectory, "announced.json"),
		Components: []Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: "v0.1.0", ChangelogPath: changelog},
		},
	}

	if err := announcer.Announce(context.Background(), []int64{7}, func(context.Context, int64, string) error {
		return nil
	}); err == nil {
		t.Fatal("Announce() error = nil, want state persistence error")
	}
}

func newTestAnnouncer(t *testing.T, version string) Announcer {
	t.Helper()
	dir := t.TempDir()
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte(testChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	return Announcer{
		StatePath: filepath.Join(dir, "announced.json"),
		Components: []Component{
			{ID: "gateway", Label: "THE GATEWAY", Version: version, ChangelogPath: changelog},
		},
	}
}
