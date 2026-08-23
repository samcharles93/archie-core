package telegram

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

var errFixtureInstall = errors.New("build failed: exit status 1")

func TestUpdateShowsComponentSectionsAndActions(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.Updates = &updateStub{snapshot: releaseupdate.Snapshot{Components: []releaseupdate.Component{
		{ID: "gateway", Label: "THE GATEWAY", Installed: "v0.1.0", Available: "v0.1.1", Changelog: "- Clearer help"},
		{ID: "runtime", Label: "THE RUNTIME", Installed: "v0.1.0"},
	}}}
	b, requests := newTelegramTestBot(t)

	g.updateHandler()(context.Background(), b, &models.Update{Message: &models.Message{
		From: &models.User{ID: 42}, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}, Text: "/update",
	}})

	if len(*requests) != 1 || (*requests)[0].method != "sendMessage" {
		t.Fatalf("requests = %#v, want update message", *requests)
	}
	text := (*requests)[0].form["text"]
	for _, want := range []string{"Archie has an update available:", "--- THE GATEWAY ---", "v0.1.1 available", "- Clearer help", "--- THE RUNTIME ---", "No runtime changes"} {
		if !strings.Contains(text, want) {
			t.Errorf("update text missing %q: %q", want, text)
		}
	}
	if !strings.Contains((*requests)[0].form["reply_markup"], updateApproveCallback) || !strings.Contains((*requests)[0].form["reply_markup"], updateDeferCallback) {
		t.Errorf("update keyboard = %q", (*requests)[0].form["reply_markup"])
	}
}

// TestInstallUpdateStreamsProgressAndReportsSuccess: the phase-1 summary
// must be distinct from a final "it worked" claim -- the daemon has not
// restarted yet when this message is sent, so it can only promise that the
// build/install step succeeded and a restart is queued.
func TestInstallUpdateStreamsProgressAndReportsSuccess(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	stub := &updateStub{
		progressText: []string{"==> fetching", "==> building"},
		result: releaseupdate.Result{
			Previous:         map[string]string{releaseupdate.ComponentDaemon: "1.0.0", releaseupdate.ComponentAgent: "1.0.0"},
			Installed:        map[string]string{releaseupdate.ComponentDaemon: "1.1.0", releaseupdate.ComponentAgent: "1.0.0"},
			RestartRequested: true,
		},
	}
	g.Updates = stub
	b, requests := newTelegramTestBot(t)

	message := &models.Message{Chat: models.Chat{ID: 7}, ID: 100, MessageThreadID: 3}
	g.installUpdate(context.Background(), b, &models.CallbackQuery{
		From:    models.User{ID: 42},
		Message: models.MaybeInaccessibleMessage{Message: message},
	}, releaseupdate.Snapshot{})

	if stub.gotMeta.Channel != "telegram" || stub.gotMeta.ChatID != 7 || stub.gotMeta.ThreadID != 3 {
		t.Errorf("gotMeta = %#v, want channel/chat/thread forwarded", stub.gotMeta)
	}
	if len(*requests) == 0 {
		t.Fatal("no requests sent")
	}
	last := (*requests)[len(*requests)-1]
	if last.method != "editMessageText" {
		t.Fatalf("last request method = %q, want editMessageText", last.method)
	}
	text := last.form["text"]
	for _, want := range []string{"1.0.0 → 1.1.0", "restart queued", "healthy"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("summary missing %q: %q", want, text)
		}
	}
}

// TestInstallUpdateReportsBuildFailureWithoutClaimingRestart: a failure
// during build/install must say nothing was restarted -- that's true only
// because the reference script never reaches the restart step on a
// non-zero exit.
func TestInstallUpdateReportsBuildFailureWithoutClaimingRestart(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	stub := &updateStub{installErr: errFixtureInstall}
	g.Updates = stub
	b, requests := newTelegramTestBot(t)

	message := &models.Message{Chat: models.Chat{ID: 7}, ID: 100}
	g.installUpdate(context.Background(), b, &models.CallbackQuery{
		From:    models.User{ID: 42},
		Message: models.MaybeInaccessibleMessage{Message: message},
	}, releaseupdate.Snapshot{})

	last := (*requests)[len(*requests)-1]
	text := strings.ToLower(last.form["text"])
	if !strings.Contains(text, "failed") {
		t.Errorf("summary missing failure notice: %q", text)
	}
	if strings.Contains(text, "restart queued") {
		t.Errorf("summary falsely claims a restart was queued: %q", text)
	}
}

// TestReportPendingUpdateSendsAndClearsReport: this is the phase-2 side of
// the two-phase update report -- called on every gateway launch (process
// boot and /restart alike), it must relay whatever the update watchdog
// decided and never report the same outcome twice.
func TestReportPendingUpdateSendsAndClearsReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-report.json")
	report := releaseupdate.Report{
		Channel: "telegram", ChatID: 7, ThreadID: 3,
		Previous: map[string]string{releaseupdate.ComponentDaemon: "1.0.0"}, Installed: map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
		HealthCheck: "passed",
	}
	if err := releaseupdate.WritePendingReport(path, report); err != nil {
		t.Fatal(err)
	}
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.UpdateReportPath = path
	b, requests := newTelegramTestBot(t)

	g.reportPendingUpdate(context.Background(), b)

	if len(*requests) != 1 {
		t.Fatalf("requests = %#v, want exactly one", *requests)
	}
	if got := (*requests)[0].form["chat_id"]; got != "7" {
		t.Errorf("chat_id = %q, want 7", got)
	}
	if _, found, err := releaseupdate.ReadPendingReport(path); err != nil || found {
		t.Errorf("report still present after reportPendingUpdate: found=%v err=%v", found, err)
	}
}

// TestReportPendingUpdateClearsUnreadableReport: a corrupt report file
// (partial write, disk issue, a future format the running binary doesn't
// understand) must not jam forever -- without clearing it, every future
// launch would retry, fail identically, and the chat that approved the
// update would never learn any outcome at all.
func TestReportPendingUpdateClearsUnreadableReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update-report.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.UpdateReportPath = path
	b, _ := newTelegramTestBot(t)

	g.reportPendingUpdate(context.Background(), b)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt report file still exists after reportPendingUpdate: err = %v", err)
	}
}

func TestReportPendingUpdateNoFileIsNoop(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.UpdateReportPath = filepath.Join(t.TempDir(), "does-not-exist.json")
	b, requests := newTelegramTestBot(t)

	g.reportPendingUpdate(context.Background(), b)

	if len(*requests) != 0 {
		t.Fatalf("requests = %#v, want none", *requests)
	}
}

func TestReportPendingUpdateDisabledWhenPathEmpty(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.reportPendingUpdate(context.Background(), b)

	if len(*requests) != 0 {
		t.Fatalf("requests = %#v, want none", *requests)
	}
}

// TestFormatPendingReportVerifiesAgainstRunningVersions is the regression
// suite for the false-success bug: the watchdog's health probe passes
// whenever *a* daemon answers it, so a report can claim an install that never
// took effect. A claim the running system contradicts must not be worded as
// success; a claim it cannot check must still be, since failing closed on
// components that simply cannot self-report would call every update a
// failure.
func TestFormatPendingReportVerifiesAgainstRunningVersions(t *testing.T) {
	const (
		daemon       = releaseupdate.ComponentDaemon
		agent        = releaseupdate.ComponentAgent
		previousVer  = "1.0.0"
		installedVer = "1.1.0"
	)
	tests := []struct {
		name      string
		previous  map[string]string
		installed map[string]string
		running   map[string]string
		wantText  []string
		denyText  []string
	}{
		{
			name:      "running the version the update replaced",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   map[string]string{daemon: previousVer},
			wantText:  []string{daemon, previousVer, installedVer, "still the version this update meant to replace"},
			denyText:  []string{"update complete", "healthy on the new version"},
		},
		{
			name:      "running a version the report never mentions",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   map[string]string{daemon: "9.9.9"},
			wantText:  []string{daemon, "9.9.9", installedVer},
			denyText:  []string{"update complete", "still the version this update meant to replace"},
		},
		{
			// A single sentence covering all components cannot be true of a
			// mixed set: the daemon here IS on the version the update meant
			// to replace and the agent is on neither, so the explanation has
			// to attach per component or it states a falsehood about one of
			// them. The trailing semicolon in the agent assertion is what
			// proves the annotation did not leak onto it.
			name:      "mixed drift annotates each component separately",
			previous:  map[string]string{daemon: previousVer, agent: previousVer},
			installed: map[string]string{daemon: installedVer, agent: installedVer},
			running:   map[string]string{daemon: previousVer, agent: "9.9.9"},
			wantText: []string{
				"agent reports 9.9.9, not the installed " + installedVer + ";",
				"daemon reports " + previousVer + ", not the installed " + installedVer +
					" (still the version this update meant to replace)",
			},
			denyText: []string{"update complete"},
		},
		{
			name:      "claim confirmed by the running version",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   map[string]string{daemon: installedVer},
			wantText:  []string{"update complete"},
			denyText:  []string{"did not take effect"},
		},
		{
			// The ordinary production shape: only the daemon can self-report,
			// so a successful update always confirms the daemon and leaves
			// the agent unchecked. The headline is earned, but the version
			// line above it covers the agent too, so the message has to say
			// which components nothing actually verified -- otherwise the
			// confirmed daemon silently vouches for the agent.
			name:      "component that cannot self-report is named, not treated as drift",
			previous:  map[string]string{daemon: previousVer, agent: previousVer},
			installed: map[string]string{daemon: installedVer, agent: installedVer},
			running:   map[string]string{daemon: installedVer},
			wantText:  []string{"update complete", "not independently checked: " + agent},
			denyText:  []string{"did not take effect"},
		},
		{
			name:      "nothing is left unchecked when every component confirms",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   map[string]string{daemon: installedVer},
			wantText:  []string{"healthy on the new version"},
			denyText:  []string{"not independently checked"},
		},
		{
			name:      "nothing self-reports, so success is reported unconfirmed",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   nil,
			wantText:  []string{"could not confirm which version"},
			denyText:  []string{"did not take effect", "healthy on the new version"},
		},
		{
			name:      "an unrecorded claim is not a contradiction, but is not confirmed either",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: releaseupdate.VersionUnknown},
			running:   map[string]string{daemon: installedVer},
			wantText:  []string{"could not confirm which version"},
			denyText:  []string{"did not take effect", "healthy on the new version", releaseupdate.VersionUnknown},
		},
		{
			name:      "an unstamped running build cannot confirm or contradict",
			previous:  map[string]string{daemon: previousVer},
			installed: map[string]string{daemon: installedVer},
			running:   map[string]string{daemon: releaseupdate.VersionDev},
			wantText:  []string{"could not confirm which version"},
			denyText:  []string{"did not take effect", "healthy on the new version"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := releaseupdate.Report{
				Previous:    test.previous,
				Installed:   test.installed,
				HealthCheck: "passed",
			}
			text := formatPendingReport(report, test.running)
			lower := strings.ToLower(text)
			for _, want := range test.wantText {
				if !strings.Contains(lower, strings.ToLower(want)) {
					t.Errorf("report does not mention %q: %q", want, text)
				}
			}
			for _, deny := range test.denyText {
				if strings.Contains(lower, strings.ToLower(deny)) {
					t.Errorf("report wrongly claims %q: %q", deny, text)
				}
			}
		})
	}
}

func TestFormatPendingReportDescribesSuccessAndRollback(t *testing.T) {
	success := releaseupdate.Report{
		Previous:    map[string]string{releaseupdate.ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
		HealthCheck: "passed",
		RolledBack:  false,
	}
	text := formatPendingReport(success, map[string]string{releaseupdate.ComponentDaemon: "1.1.0"})
	for _, want := range []string{"1.0.0 → 1.1.0", "healthy"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("success report missing %q: %q", want, text)
		}
	}

	failure := releaseupdate.Report{
		Previous:    map[string]string{releaseupdate.ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
		HealthCheck: "failed",
		RolledBack:  true,
	}
	text = formatPendingReport(failure, map[string]string{releaseupdate.ComponentDaemon: "1.0.0"})
	for _, want := range []string{"rolled back", "1.0.0"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("rollback report missing %q: %q", want, text)
		}
	}
}

// TestFormatPendingReportDistinguishesUnrolledBackFailure: a failed health
// check with no backup to restore from must never read like the success or
// rolled-back cases -- the daemon may still be running the broken version,
// or may not be running at all, and nothing further will fix that
// automatically. Reporting this the same as a successful rollback is worse
// than reporting nothing, because it tells the operator to stand down.
func TestFormatPendingReportDistinguishesUnrolledBackFailure(t *testing.T) {
	notRolledBack := releaseupdate.Report{
		Previous:    map[string]string{releaseupdate.ComponentDaemon: "1.0.0"},
		Installed:   map[string]string{releaseupdate.ComponentDaemon: "1.1.0"},
		HealthCheck: "failed",
		RolledBack:  false,
	}
	text := strings.ToLower(formatPendingReport(notRolledBack, map[string]string{releaseupdate.ComponentDaemon: "1.1.0"}))
	if strings.Contains(text, "was automatically rolled back") {
		t.Errorf("report falsely claims a rollback happened: %q", text)
	}
	if strings.Contains(text, "running normally") {
		t.Errorf("report falsely claims the system is healthy: %q", text)
	}
	for _, want := range []string{"could not", "manual"} {
		if !strings.Contains(text, want) {
			t.Errorf("report missing %q (must call out that manual intervention is needed): %q", want, text)
		}
	}
}

func TestUpdateCallbackRejectsUnauthorizedSender(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.Updates = &updateStub{}
	b, requests := newTelegramTestBot(t)

	g.handleUpdateCallback(context.Background(), b, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "callback", From: models.User{ID: 99}, Data: updateApproveCallback,
	}})

	stub, ok := g.Updates.(*updateStub)
	if !ok {
		t.Fatalf("g.Updates is %T, want *updateStub", g.Updates)
	}
	if stub.installCalls != 0 {
		t.Error("unauthorized callback started an installation")
	}
	if len(*requests) != 1 || (*requests)[0].method != "answerCallbackQuery" {
		t.Fatalf("requests = %#v, want authorization response", *requests)
	}
}

type updateStub struct {
	snapshot     releaseupdate.Snapshot
	installCalls int
	gotMeta      releaseupdate.InstallMeta
	result       releaseupdate.Result
	installErr   error
	progressText []string
}

func (s *updateStub) Check(context.Context, int64) (releaseupdate.Snapshot, error) {
	return s.snapshot, nil
}
func (s *updateStub) Defer(context.Context, int64, releaseupdate.Snapshot) error { return nil }
func (s *updateStub) Install(_ context.Context, _ releaseupdate.Snapshot, meta releaseupdate.InstallMeta, progress func(string)) (releaseupdate.Result, error) {
	s.installCalls++
	s.gotMeta = meta
	for _, line := range s.progressText {
		progress(line)
	}
	if s.installErr != nil {
		return releaseupdate.Result{}, s.installErr
	}
	return s.result, nil
}
func (*updateStub) CanInstall() bool { return true }
