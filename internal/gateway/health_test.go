package gateway

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fakeHealthSource is the seam the daemon supplies in production; the report
// it returns is fixed so each case pins one rendering rule.
type fakeHealthSource struct {
	report HealthReport
}

func (f fakeHealthSource) Health() HealthReport { return f.report }

// TestFormatHealthRendersOnlyWhatWasReported pins the rule that makes /status
// trustworthy: a section the reporting process has no truthful source for is
// omitted, never rendered as a zero, a dash or a guess. A health surface that
// invents a value is worse than a thin one, because an operator cannot tell
// an invented "0" from a measured one.
func TestFormatHealthRendersOnlyWhatWasReported(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	past := func(d time.Duration) time.Time { return now.Add(-d) }
	at := func(d time.Duration) *time.Time { v := past(d); return &v }
	// The zero time is the daemon's "no pass has started yet" value, distinct
	// from a stale timestamp.
	var zeroTime time.Time

	tests := []struct {
		name   string
		report HealthReport
		want   string
	}{
		{
			name:   "no source wired reports nothing at all",
			report: HealthReport{},
			want:   "",
		},
		{
			name:   "disconnected broker",
			report: HealthReport{Broker: &BrokerHealth{}},
			want:   "Broker: disconnected",
		},
		{
			name:   "connected broker",
			report: HealthReport{Broker: &BrokerHealth{Connected: true}},
			want:   "Broker: connected",
		},
		{
			name:   "worker pool with a configured cap",
			report: HealthReport{Containers: &ContainerHealth{Active: 1, Cap: 4}},
			want:   "Containers: 1/4 active",
		},
		{
			name: "worker pool with no configured cap renders no ratio",
			// MaxConcurrency <= 0 means unlimited, so "2/0" would read as
			// "at capacity" when it means the opposite.
			report: HealthReport{Containers: &ContainerHealth{Active: 2}},
			want:   "Containers: 2 active",
		},
		{
			name: "empty pool with a cap",
			report: HealthReport{
				Containers: &ContainerHealth{Cap: 3},
			},
			want: "Containers: 0/3 active",
		},
		{
			name: "channels report their own lifecycle state",
			report: HealthReport{Channels: []ChannelHealth{
				{Name: "telegram", State: "running"},
				{Name: "email", State: "failed", Detail: "dial tcp 127.0.0.1:2525: connection refused"},
			}},
			want: "Channels: telegram running · email failed (dial tcp 127.0.0.1:2525: connection refused)",
		},
		{
			name: "a multi-line channel detail stays on one line",
			report: HealthReport{Channels: []ChannelHealth{
				{Name: "telegram", State: "failed", Detail: "getMe failed:\n  401 Unauthorized"},
			}},
			want: "Channels: telegram failed (getMe failed: 401 Unauthorized)",
		},
		{
			name: "chat model with nothing attempted says so",
			report: HealthReport{
				ChatModel: &ChatModelHealth{},
			},
			want: "Chat model: no calls attempted yet",
		},
		{
			name: "last chat model call succeeded",
			report: HealthReport{
				ChatModel: &ChatModelHealth{
					Attempted: true,
					Outcome:   ChatModelOutcome{Model: "openai/gpt-5.6", At: past(30 * time.Second)},
				},
			},
			want: "Chat model: ok just now (openai/gpt-5.6)",
		},
		{
			name: "last chat model call failed, with its model and error",
			report: HealthReport{
				ChatModel: &ChatModelHealth{
					Attempted: true,
					Outcome: ChatModelOutcome{
						Model: "openai/gpt-5.6",
						At:    past(4 * time.Minute),
						Err:   "401 Unauthorized",
					},
				},
			},
			want: "Chat model: failed 4m ago (openai/gpt-5.6): 401 Unauthorized",
		},
		{
			name: "a call whose model is unknown still reports its outcome",
			report: HealthReport{
				ChatModel: &ChatModelHealth{
					Attempted: true,
					Outcome:   ChatModelOutcome{At: past(2 * time.Minute), Err: "connection reset"},
				},
			},
			want: "Chat model: failed 2m ago: connection reset",
		},
		{
			name:   "poller has not run a pass yet",
			report: HealthReport{LastPoll: &zeroTime},
			want:   "Last poll: not yet",
		},
		{
			name:   "poller last ran minutes ago",
			report: HealthReport{LastPoll: at(12 * time.Minute)},
			want:   "Last poll: 12m ago",
		},
		{
			name: "every section present renders in one stable order",
			report: HealthReport{
				Broker:     &BrokerHealth{Connected: true},
				Containers: &ContainerHealth{Active: 1, Cap: 2},
				Channels:   []ChannelHealth{{Name: "telegram", State: "running"}},
				ChatModel: &ChatModelHealth{
					Attempted: true,
					Outcome:   ChatModelOutcome{Model: "openai/gpt-5.6", At: past(time.Minute)},
				},
				LastPoll: at(5 * time.Second),
			},
			want: strings.Join([]string{
				"Broker: connected",
				"Containers: 1/2 active",
				"Channels: telegram running",
				"Chat model: ok 1m ago (openai/gpt-5.6)",
				"Last poll: just now",
				// Joined with the report's hard line break: each of these is a
				// line, and a plain "\n" soft-joins them into one paragraph
				// in a Markdown-rendering channel.
			}, reportLineBreak),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatHealth(tc.report, now); got != tc.want {
				t.Errorf("formatHealth() =\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestFormatStatusAppendsHealthToTheQueueLine pins the shape of the whole
// reply: the aggregate queue line keeps its place (Phase 1's own health
// signal), the health section follows it, and an unwired health source leaves
// the reply exactly as it was.
func TestFormatStatusAppendsHealthToTheQueueLine(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		counts map[string]int
		report HealthReport
		want   string
	}{
		{
			name:   "unwired health source",
			counts: map[string]int{"running": 1},
			want:   "📊 Archie status\n\nQueue: 1 in flight (1 running)  \nRuntime: not configured",
		},
		{
			name:   "wired health source",
			counts: map[string]int{"running": 1},
			report: HealthReport{Broker: &BrokerHealth{Connected: true}},
			want:   "📊 Archie status\n\nQueue: 1 in flight (1 running)  \nBroker: connected  \nRuntime: not configured",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatStatus(tc.counts, tc.report, nil, now)
			if got != tc.want {
				t.Errorf("formatStatus() =\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestHandleStatusReadsTheWiredHealthSource pins that /status asks the daemon
// for health rather than leaving the seam unread -- the failure mode this pins
// is a router field that exists, is set by composition, and is never called.
func TestHandleStatusReadsTheWiredHealthSource(t *testing.T) {
	tests := []struct {
		name   string
		health HealthSource
		want   string
		absent string
	}{
		{
			name:   "health source wired",
			health: fakeHealthSource{report: HealthReport{Broker: &BrokerHealth{Connected: true}}},
			want:   "Broker: connected",
		},
		{
			name:   "no health source",
			health: nil,
			absent: "Broker:",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
			r.Health = tc.health

			reply, err := r.Route(context.Background(), inbound("", "/status"))
			if err != nil {
				t.Fatalf("Route: %v", err)
			}
			if tc.want != "" && !strings.Contains(reply, tc.want) {
				t.Errorf("reply = %q, want contains %q", reply, tc.want)
			}
			if tc.absent != "" && strings.Contains(reply, tc.absent) {
				t.Errorf("reply = %q, want no %q", reply, tc.absent)
			}
		})
	}
}
