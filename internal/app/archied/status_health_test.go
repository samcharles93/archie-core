package archied

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
)

// TestProviderOutcomeRecorderRecordsTheLastCallOnly pins the producer behind
// /status' chat-model line: it reports what actually happened, and the moment
// something else happens it reports that instead. There is no probe anywhere
// in this path -- a chat command must not pay a network round trip, and a
// probe would answer for the probe's connection, not the daemon's.
func TestProviderOutcomeRecorderRecordsTheLastCallOnly(t *testing.T) {
	failure := errors.New("401 Unauthorized")

	tests := []struct {
		name          string
		record        func(*providerOutcomeRecorder)
		wantAttempted bool
		wantErr       string
		wantModel     string
	}{
		{
			name:          "nothing recorded yet",
			record:        func(*providerOutcomeRecorder) {},
			wantAttempted: false,
		},
		{
			name:          "successful call",
			record:        func(r *providerOutcomeRecorder) { r.record("openai/gpt-5.6", nil) },
			wantAttempted: true,
			wantModel:     "openai/gpt-5.6",
		},
		{
			name:          "failed call",
			record:        func(r *providerOutcomeRecorder) { r.record("openai/gpt-5.6", failure) },
			wantAttempted: true,
			wantErr:       "401 Unauthorized",
			wantModel:     "openai/gpt-5.6",
		},
		{
			name: "a later success replaces an earlier failure",
			record: func(r *providerOutcomeRecorder) {
				r.record("openai/gpt-5.6", failure)
				r.record("deepseek/deepseek-v4-pro", nil)
			},
			wantAttempted: true,
			wantModel:     "deepseek/deepseek-v4-pro",
		},
		{
			name: "a later failure replaces an earlier success",
			record: func(r *providerOutcomeRecorder) {
				r.record("openai/gpt-5.6", nil)
				r.record("openai/gpt-5.6", failure)
			},
			wantAttempted: true,
			wantErr:       "401 Unauthorized",
			wantModel:     "openai/gpt-5.6",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := newProviderOutcomeRecorder()
			before := time.Now().Add(-time.Second)
			tc.record(recorder)

			outcome, attempted := recorder.LastChatModelOutcome()
			if attempted != tc.wantAttempted {
				t.Fatalf("LastChatModelOutcome() attempted = %v, want %v", attempted, tc.wantAttempted)
			}
			if !tc.wantAttempted {
				return
			}
			if outcome.Err != tc.wantErr {
				t.Errorf("outcome.Err = %q, want %q", outcome.Err, tc.wantErr)
			}
			if outcome.Model != tc.wantModel {
				t.Errorf("outcome.Model = %q, want %q", outcome.Model, tc.wantModel)
			}
			if outcome.At.Before(before) || outcome.At.After(time.Now()) {
				t.Errorf("outcome.At = %v, want a time within the call's window", outcome.At)
			}
		})
	}

	// sendChatTurn is written to tolerate a nil recorder (a call path built
	// without one must not panic), and a nil recorder must claim nothing.
	var absent *providerOutcomeRecorder
	absent.record("openai/gpt-5.6", failure)
	if _, attempted := absent.LastChatModelOutcome(); attempted {
		t.Error("a nil recorder reported an attempted call; it has nothing to report")
	}
}

// TestSendChatTurnRecordsEachCallOutcome pins the wire between the chat path
// and /status: sendChatTurn is the single point every chat-model call in this
// process passes through, and it must record what happened there rather than
// trusting each caller to remember. A recorder that is never written is the
// failure this pins -- /status would say "no calls attempted yet" forever
// while chat worked, which reads as a broken provider rather than a broken
// wire.
func TestSendChatTurnRecordsEachCallOutcome(t *testing.T) {
	tests := []struct {
		name    string
		reply   string
		status  int
		wantErr string
	}{
		{
			name:   "successful call",
			reply:  `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"gpt-5.6","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			status: http.StatusOK,
		},
		{
			name:    "failed call",
			reply:   `{"error":{"message":"invalid api key","type":"invalid_request_error"}}`,
			status:  http.StatusUnauthorized,
			wantErr: "invalid api key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requestedPath string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestedPath = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.reply)
			}))
			t.Cleanup(api.Close)

			llm := agentexec.NewRuntime(map[string]agentexec.Provider{
				"openai": {Class: "openai", BaseURL: api.URL},
			})
			if llm == nil {
				t.Fatal("NewRuntime = nil, want a runtime for the configured provider")
			}

			recorder := newProviderOutcomeRecorder()
			_, err := sendChatTurn(t.Context(), llm, "openai/gpt-5.6", core.GenerateOptions{
				Messages: []chat.Message{{Role: chat.RoleUser, Content: "hi"}},
				MaxSteps: 1,
			}, nil, recorder)

			if tc.wantErr == "" && err != nil {
				t.Fatalf("sendChatTurn = %v, want success (request path %q)", err, requestedPath)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("sendChatTurn = %v, want an error containing %q", err, tc.wantErr)
			}

			outcome, attempted := recorder.LastChatModelOutcome()
			if !attempted {
				t.Fatal("recorded outcome = attempted false, want the call recorded at sendChatTurn")
			}
			if outcome.Model != "openai/gpt-5.6" {
				t.Errorf("outcome.Model = %q, want the model the call used", outcome.Model)
			}
			if got := outcome.Err != ""; got != (tc.wantErr != "") {
				t.Errorf("outcome.Err = %q, want an error report = %v", outcome.Err, tc.wantErr != "")
			}
		})
	}
}

// TestStatusHealthOmitsSourcesThisProcessDoesNotOwn pins "truthful or
// absent": with no subsystem wired, the report is empty rather than full of
// zeroes that read as measurements. This is the shape the standalone Gateway
// process produces -- it owns no container pool, no poll loop and no channel
// manager, and must not pretend otherwise.
func TestStatusHealthOmitsSourcesThisProcessDoesNotOwn(t *testing.T) {
	report := statusHealth{}.Health()

	if report.Broker != nil {
		t.Errorf("Broker = %+v, want nil when this process holds no broker connection", report.Broker)
	}
	if report.Containers != nil {
		t.Errorf("Containers = %+v, want nil when this process owns no container pool", report.Containers)
	}
	if report.ChatModel != nil {
		t.Errorf("ChatModel = %+v, want nil when this process records no model calls", report.ChatModel)
	}
	if report.Channels != nil {
		t.Errorf("Channels = %+v, want nil when this process owns no channel manager", report.Channels)
	}
	if report.LastPoll != nil {
		t.Errorf("LastPoll = %+v, want nil when this process does not poll", report.LastPoll)
	}
}

// TestStatusHealthReportsEveryWiredSource pins that each wired source reaches
// the report, and that a source reporting "nothing has happened yet" stays
// distinguishable from an absent source.
func TestStatusHealthReportsEveryWiredSource(t *testing.T) {
	polled := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	recorder := newProviderOutcomeRecorder()
	recorder.record("openai/gpt-5.6", errors.New("401 Unauthorized"))

	channels := status.NewManager([]status.Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: true},
		{ID: "email", Configured: true},
	})
	channels.MarkRunning("telegram")
	channels.MarkFailed("email", "dial tcp: connection refused")

	tests := []struct {
		name  string
		check func(*testing.T, gateway.HealthReport)
		src   statusHealth
	}{
		{
			name: "broker",
			src:  statusHealth{broker: func() (bool, bool) { return true, true }},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.Broker == nil || !r.Broker.Connected {
					t.Errorf("Broker = %+v, want connected", r.Broker)
				}
			},
		},
		{
			name: "container pool",
			src:  statusHealth{containers: func() (int, int, bool) { return 2, 4, true }},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.Containers == nil || r.Containers.Active != 2 || r.Containers.Cap != 4 {
					t.Errorf("Containers = %+v, want 2/4", r.Containers)
				}
			},
		},
		{
			name: "channel manager",
			src:  statusHealth{channels: func() ([]gateway.ChannelHealth, bool) { return channelHealth(channels), true }},
			check: func(t *testing.T, r gateway.HealthReport) {
				want := []gateway.ChannelHealth{
					{Name: "Telegram", State: "running"},
					{Name: "email", State: "failed", Detail: "dial tcp: connection refused"},
				}
				if len(r.Channels) != len(want) {
					t.Fatalf("Channels = %+v, want %+v", r.Channels, want)
				}
				for i := range want {
					if r.Channels[i] != want[i] {
						t.Errorf("Channels[%d] = %+v, want %+v", i, r.Channels[i], want[i])
					}
				}
			},
		},
		{
			name: "chat model recorder",
			src:  statusHealth{chatModel: recorder},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.ChatModel == nil || !r.ChatModel.Attempted {
					t.Fatalf("ChatModel = %+v, want the recorded call", r.ChatModel)
				}
				if r.ChatModel.Outcome.Err != "401 Unauthorized" {
					t.Errorf("ChatModel.Outcome.Err = %q, want the recorded error", r.ChatModel.Outcome.Err)
				}
			},
		},
		{
			name: "chat model recorder with no calls yet",
			src:  statusHealth{chatModel: newProviderOutcomeRecorder()},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.ChatModel == nil {
					t.Fatal("ChatModel = nil, want a present section saying nothing has been attempted")
				}
				if r.ChatModel.Attempted {
					t.Error("ChatModel.Attempted = true, want false before any call")
				}
			},
		},
		{
			name: "poll loop that has run",
			src:  statusHealth{lastPoll: func() (time.Time, bool) { return polled, true }},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.LastPoll == nil || !r.LastPoll.Equal(polled) {
					t.Errorf("LastPoll = %v, want %v", r.LastPoll, polled)
				}
			},
		},
		{
			name: "poll loop that has not run a pass yet",
			src:  statusHealth{lastPoll: func() (time.Time, bool) { return time.Time{}, true }},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.LastPoll == nil {
					t.Fatal("LastPoll = nil, want a present \"not yet\" value")
				}
				if !r.LastPoll.IsZero() {
					t.Errorf("LastPoll = %v, want the zero time", r.LastPoll)
				}
			},
		},
		{
			name: "poll loop that is not this process's to report",
			src:  statusHealth{lastPoll: func() (time.Time, bool) { return polled, false }},
			check: func(t *testing.T, r gateway.HealthReport) {
				if r.LastPoll != nil {
					t.Errorf("LastPoll = %v, want nil", r.LastPoll)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, tc.src.Health())
		})
	}
}

// TestStatusHealthReadsDeferredSourcesAtCallTime pins the composition-order
// trap this source sits behind: the daemon is built AFTER the gateways
// (main.go runs setupGateways before buildDaemon), so the health source is
// constructed while boot.d is still nil. A source that captured boot.d at
// construction would report the poll section as permanently absent -- /status
// would look wired and never say anything about polling.
func TestStatusHealthReadsDeferredSourcesAtCallTime(t *testing.T) {
	b := &boot{}
	src := newStatusHealth(b)

	if got := src.Health().LastPoll; got != nil {
		t.Fatalf("LastPoll = %v with no daemon yet, want nil", got)
	}

	b.d = &daemon.Daemon{}

	got := src.Health().LastPoll
	if got == nil {
		t.Fatal("LastPoll = nil after the daemon was built, want the source to read boot.d at call time")
	}
	if !got.IsZero() {
		t.Errorf("LastPoll = %v, want the zero time: no poll pass has run yet", got)
	}
}

// TestChannelHealthProjectsManagerSnapshot pins the mapping from the channel
// manager's own state to what /status renders -- including the two edges an
// operator would otherwise have to guess at: a channel with no display name
// shows its id, and an unconfigured channel is reported as such rather than
// omitted.
func TestChannelHealthProjectsManagerSnapshot(t *testing.T) {
	manager := status.NewManager([]status.Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: true},
		{ID: "email", Configured: true},
		{ID: "webhook"},
	})
	manager.MarkStarting("telegram")
	manager.MarkRunning("telegram")
	manager.MarkFailed("email", "listen tcp: address already in use")

	tests := []struct {
		name    string
		manager *status.Manager
		want    []gateway.ChannelHealth
	}{
		{
			name:    "no channel manager",
			manager: nil,
			want:    nil,
		},
		{
			name:    "manager snapshot",
			manager: manager,
			want: []gateway.ChannelHealth{
				{Name: "Telegram", State: "running"},
				{Name: "email", State: "failed", Detail: "listen tcp: address already in use"},
				{Name: "webhook", State: "stopped"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := channelHealth(tc.manager)
			if len(got) != len(tc.want) {
				t.Fatalf("channelHealth() = %+v, want %+v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("channelHealth()[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestBuildTelegramRouterCarriesStatusHealth pins the last hop: a router built
// with a health source must expose it, or /status answers from the queue and
// runtime sections alone while the composition root believes health is wired.
func TestBuildTelegramRouterCarriesStatusHealth(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	st := store.OpenTest(t)
	src := statusHealth{broker: func() (bool, bool) { return true, true }}
	router := buildTelegramRouter(context.Background(), nil, telegramSetup{
		Cfg:          config.NewHolder(config.Config{}),
		St:           st,
		SessionStore: sessions,
		StatusHealth: src,
		Log:          slog.Default(),
	}, sessions)

	if router.Health == nil {
		t.Fatal("router.Health = nil, want the configured health source")
	}
	reply, err := router.Route(context.Background(), gateway.Inbound{
		Message: messaging.Message{Role: messaging.RoleUser, Text: "/status"},
	})
	if err != nil {
		t.Fatalf("Route(/status): %v", err)
	}
	if !strings.Contains(reply, "Broker: connected") {
		t.Errorf("reply = %q, want the wired broker line", reply)
	}
}
