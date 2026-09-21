package archied

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	natsio "github.com/nats-io/nats.go"
	"github.com/samcharles93/ai-sdk/chat"
	"github.com/samcharles93/ai-sdk/core"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/container"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/curator"
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

// TestStatusHealthReportsOnlyBrokerAndChatModel pins what every /status
// surface actually serves. The only production router.Health is the Gateway's,
// and the facts the Gateway holds are the ones this boot is given here. A
// producer wired to the pool or the poll loop would build a section nothing in
// this process can serve -- the operator would read a full health list in a
// process that has no container pool, and would read it as measured.
func TestStatusHealthReportsOnlyBrokerAndChatModel(t *testing.T) {
	srv := startEmbeddedNATS(t)
	conn, err := natsio.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(conn.Close)

	recorder := newProviderOutcomeRecorder()
	recorder.record("openai/gpt-5.6", nil)

	// The pool and the daemon are present here on purpose: the daemon
	// composition holds both, so a producer reading them would claim a
	// container and poll line from a process whose router cannot serve it.
	b := &boot{
		taskActionsConn:  conn,
		providerOutcomes: recorder,
		containerPool:    &container.Pool{},
		d:                &daemon.Daemon{},
	}

	report := newStatusHealth(b).Health()

	if report.Broker == nil || !report.Broker.Connected {
		t.Errorf("Broker = %+v, want this process's own connected broker connection", report.Broker)
	}
	if report.ChatModel == nil || !report.ChatModel.Attempted {
		t.Errorf("ChatModel = %+v, want the recorded call", report.ChatModel)
	}
	if report.Containers != nil {
		t.Errorf("Containers = %+v, want nil: no /status surface is served from this boot's pool", report.Containers)
	}
	if report.Channels != nil {
		t.Errorf("Channels = %+v, want nil: no /status surface is served from this boot's channels", report.Channels)
	}
	if report.LastPoll != nil {
		t.Errorf("LastPoll = %v, want nil: no /status surface is served from this boot's poll loop", report.LastPoll)
	}
}

// TestStatusHealthReportsEveryWiredSource pins that each wired source reaches
// the report, and that a source reporting "nothing has happened yet" stays
// distinguishable from an absent source.
func TestStatusHealthReportsEveryWiredSource(t *testing.T) {
	recorder := newProviderOutcomeRecorder()
	recorder.record("openai/gpt-5.6", errors.New("401 Unauthorized"))

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, tc.src.Health())
		})
	}
}

// TestSetupGatewayChatCarriesStatusHealth pins the last hop: the router the
// composition root builds must expose the health source, or /status answers
// from the queue and runtime sections alone while the composition root
// believes health is wired.
//
// Asserted on the source rather than by calling setupGatewayChat, which needs
// a fully built boot (stores, runtime, memory engines) to run. That is the
// same technique composition_order_test.go uses for wiring nothing at runtime
// observes. The predecessor of this test drove buildTelegramRouter, which the
// Messaging Service extraction deleted along with the daemon's own routers;
// setupGatewayChat is now the sole production constructor of a Router.
func TestSetupGatewayChatCarriesStatusHealth(t *testing.T) {
	fileset := token.NewFileSet()
	file, err := parser.ParseFile(fileset, "gateway_runtime.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	body := methodBody(t, file, "setupGatewayChat")

	var wired bool
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 {
			return true
		}
		sel, ok := assign.Lhs[0].(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Health" {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "router" {
			wired = true
		}
		return true
	})
	if !wired {
		t.Fatal("setupGatewayChat never assigns router.Health; /status would lose its health section")
	}
}

// TestRouterWithHealthAnswersStatus is the behavioural half: a router holding a
// health source renders it into /status.
func TestRouterWithHealthAnswersStatus(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	st := store.OpenTest(t)
	router := gateway.NewRouter(st, nil, "web")
	router.InitSessions(sessions)
	router.Health = statusHealth{broker: func() (bool, bool) { return true, true }}

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

// TestRecordKeepsTheLatestOutcomeUnderReordering is the regression case for a
// /status chat-model line that reports an older call as the latest. The
// completion time is taken before the lock, so two concurrent calls can reach
// the lock in the opposite order to their completion; an unconditional store
// then overwrites the newer outcome with the older one.
func TestRecordKeepsTheLatestOutcomeUnderReordering(t *testing.T) {
	r := &providerOutcomeRecorder{}
	newer := time.Now().UTC()
	older := newer.Add(-time.Minute)

	// The later call completes and stores first, as it would when the earlier
	// call is descheduled between stamping and locking.
	r.recordAt("openai/gpt-5.6", nil, newer)
	r.recordAt("openai/gpt-4o", errors.New("401 Unauthorized"), older)

	got, attempted := r.LastChatModelOutcome()
	if !attempted {
		t.Fatal("LastChatModelOutcome reports no attempt after two records")
	}
	if got.Model != "openai/gpt-5.6" || got.Err != "" {
		t.Fatalf("outcome = %+v, want the newer successful call to survive", got)
	}
}

// TestRecordStoresTheFirstOutcomeWhateverItsAge pins that the ordering guard
// does not swallow the first record, whose timestamp has nothing to compare
// against.
func TestRecordStoresTheFirstOutcomeWhateverItsAge(t *testing.T) {
	r := &providerOutcomeRecorder{}
	r.recordAt("openai/gpt-4o", nil, time.Now().UTC().Add(-time.Hour))

	if _, attempted := r.LastChatModelOutcome(); !attempted {
		t.Fatal("the first recorded outcome was dropped")
	}
}

// TestCuratorRunnerRefusesWithoutARuntime pins that a curator asking for a
// completion on a daemon with no configured provider gets an error. The
// runtime constructor returns nil in that case, and a nil *runtime.Runtime
// panics on its first method call, which would take the daemon down. Nothing
// is recorded: no provider call was attempted, and saying one failed would be
// a /status that blames the provider for a local misconfiguration.
func TestCuratorRunnerRefusesWithoutARuntime(t *testing.T) {
	recorder := &providerOutcomeRecorder{}
	runner := curatorLLMRunner{outcomes: recorder}

	if _, err := runner.Chat(context.Background(), curator.ChatRequest{Model: "openai/gpt-4o"}); err == nil {
		t.Fatal("Chat with no runtime returned no error")
	}
	if _, attempted := recorder.LastChatModelOutcome(); attempted {
		t.Error("a local misconfiguration was recorded as a provider call")
	}
}

// TestCuratorRunnerRecordsItsOutcome pins that curator model calls reach
// /status. They do not pass through sendChatTurn, so before this a daemon
// whose only recent model traffic was curator work reported "no calls
// attempted yet" while the provider was demonstrably reachable.
func TestCuratorRunnerRecordsItsOutcome(t *testing.T) {
	recorder := &providerOutcomeRecorder{}
	// A provider pointed at a closed local port fails fast without touching
	// the network. The outcome is what matters here: a failed provider call is
	// exactly what /status must not miss.
	rt := agentexec.NewRuntime(map[string]agentexec.Provider{
		"openai": {Class: "openai", BaseURL: "http://127.0.0.1:1", APIKeyEnv: "ARCHIE_TEST_MISSING_KEY"},
	})
	if rt == nil {
		t.Fatal("NewRuntime returned nil for a configured provider")
	}
	runner := curatorLLMRunner{rt: rt, outcomes: recorder}

	_, err := runner.Chat(context.Background(), curator.ChatRequest{Model: "openai/gpt-4o"})
	if err == nil {
		t.Fatal("Chat against a closed port returned no error")
	}

	got, attempted := recorder.LastChatModelOutcome()
	if !attempted {
		t.Fatal("curator call was not recorded; /status would report no calls attempted")
	}
	if got.Model != "openai/gpt-4o" || got.Err == "" {
		t.Fatalf("outcome = %+v, want the failed curator call", got)
	}
}

// TestTitleGenerationDoesNotRecordChatModelHealth is the regression case for
// /status blaming the provider for a cosmetic failure. Title generation runs
// detached after a first turn under its own 30s bound and its error is
// swallowed by the caller, so a title that merely timed out must not become
// the process-wide "Chat model: failed" that an operator reads as an outage.
func TestTitleGenerationDoesNotRecordChatModelHealth(t *testing.T) {
	recorder := &providerOutcomeRecorder{}
	rt := agentexec.NewRuntime(map[string]agentexec.Provider{
		"openai": {Class: "openai", BaseURL: "http://127.0.0.1:1", APIKeyEnv: "ARCHIE_TEST_MISSING_KEY"},
	})
	if rt == nil {
		t.Fatal("NewRuntime returned nil for a configured provider")
	}
	gen := &chatTitleGenerator{llm: rt, chatModels: newChatModelManager(map[string]string{"chat": "openai/gpt-4o"}, nil, nil)}

	if _, err := gen.GenerateTitle(context.Background(), "s1", "hello"); err == nil {
		t.Fatal("GenerateTitle against a closed port returned no error")
	}
	if _, attempted := recorder.LastChatModelOutcome(); attempted {
		t.Error("a failed title proposal was recorded as chat-model health")
	}
}
