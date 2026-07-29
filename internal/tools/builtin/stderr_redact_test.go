package builtin

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// --- G16: stderr redaction, rate limiting, capture cap ---

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "api key env pattern",
			in:   "TAU_API_KEY=sk-abc123testtesttesttest",
			want: "TAU_API_KEY=[REDACTED]",
		},
		{
			name: "bearer token, spec's exact example",
			in:   "Authorization: Bearer tok_abc123",
			want: "Authorization: Bearer [REDACTED]",
		},
		{
			name: "bare sk- key",
			in:   "leaked key: sk-abcdefghijklmnopqrstuvwxyz",
			want: "leaked key: [REDACTED]",
		},
		{
			name: "generic secret env var",
			in:   "DATABASE_PASSWORD=hunter2",
			want: "DATABASE_PASSWORD=[REDACTED]",
		},
		{
			name: "generic token env var",
			in:   "GITHUB_TOKEN=ghp_abcdefghijklmnop",
			want: "GITHUB_TOKEN=[REDACTED]",
		},
		{
			name: "no secret present",
			in:   "starting build in /home/user/project",
			want: "starting build in /home/user/project",
		},
		{
			name: "ordinary key=value that is not sensitive",
			in:   "BUILD_MODE=release",
			want: "BUILD_MODE=release",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactSecrets(tc.in)
			if got != tc.want {
				t.Errorf("redactSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDrainChildStderr_RedactsBeforeLogging(t *testing.T) {
	var records []slog.Record
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(captureHandler{records: &records}))

	r := strings.NewReader("TAU_API_KEY=sk-abc123testtesttesttest\n")
	drainChildStderr(r, "tau#test01")

	if len(records) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(records))
	}
	if strings.Contains(records[0].Message, "sk-abc123") {
		t.Errorf("secret leaked into log: %q", records[0].Message)
	}
	if !strings.Contains(records[0].Message, "[REDACTED]") {
		t.Errorf("expected redacted message, got %q", records[0].Message)
	}
}

func TestDrainChildStderr_CapsTotalCapture(t *testing.T) {
	var records []slog.Record
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(captureHandler{records: &records}))

	// Each line is 100 bytes; write enough lines to exceed the 64KiB cap.
	// Uses a fake clock that jumps a full rate-limit window forward per
	// line, so the rate limiter never engages and only the capture cap is
	// under test - isolating it from TestDrainChildStderr_RateLimitsBurstInOneWindow.
	line := strings.Repeat("x", 99)
	var sb strings.Builder
	lineCount := (stderrMaxCaptureBytes / 100) + 50
	for range lineCount {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	fakeNow := time.Now()
	clock := func() time.Time {
		fakeNow = fakeNow.Add(stderrRateLimitWindow)
		return fakeNow
	}
	drainChildStderrWithClock(strings.NewReader(sb.String()), "tau#test02", clock)

	var truncationMarkers int
	for _, rec := range records {
		if strings.Contains(rec.Message, "capture limit exceeded") {
			truncationMarkers++
		}
	}
	if truncationMarkers != 1 {
		t.Errorf("expected exactly 1 capture-limit truncation marker, got %d", truncationMarkers)
	}
	if len(records) >= lineCount {
		t.Errorf("expected fewer log records than input lines (cap should stop logging), got %d records for %d lines", len(records), lineCount)
	}
}

func TestDrainChildStderr_RateLimitsBurstInOneWindow(t *testing.T) {
	var records []slog.Record
	orig := slog.Default()
	defer slog.SetDefault(orig)
	slog.SetDefault(slog.New(captureHandler{records: &records}))

	// Well over the 4096*5=20480 byte per-window budget, all within the
	// same instant (no real time passes reading from a strings.Reader).
	line := strings.Repeat("y", 999)
	var sb strings.Builder
	for range 50 { // ~50KB total, comfortably over the 20480 budget
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	drainChildStderr(strings.NewReader(sb.String()), "tau#test03")

	var rateLimitMarkers int
	for _, rec := range records {
		if strings.Contains(rec.Message, "rate limit exceeded") {
			rateLimitMarkers++
		}
	}
	if rateLimitMarkers != 1 {
		t.Errorf("expected exactly 1 rate-limit truncation marker, got %d", rateLimitMarkers)
	}
	if len(records) >= 50 {
		t.Errorf("expected rate limiting to suppress most of the 50 lines, got %d records", len(records))
	}
}

// captureHandler is a minimal slog.Handler that records every Record it
// receives, for asserting on log content without depending on slog's text
// formatting.
type captureHandler struct {
	records *[]slog.Record
}

func (captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, r)
	return nil
}

func (h captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }

func (h captureHandler) WithGroup(_ string) slog.Handler { return h }
