package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

type chatStatusStub struct{}

func (chatStatusStub) StatusCounts(context.Context) (map[string]int, error) {
	return map[string]int{"queued": 2}, nil
}

type chatProviderModelStub struct {
	active string
}

func (s *chatProviderModelStub) Models() []string {
	return []string{"openai/gpt-5", "openrouter/sonnet"}
}

func (s *chatProviderModelStub) ActiveModel() string { return s.active }

func (s *chatProviderModelStub) SetActiveModel(context.Context, string) error { return nil }

func (*chatProviderModelStub) Providers() []string { return []string{"openai", "openrouter"} }

func (s *chatProviderModelStub) ActiveProvider() string {
	provider, _, _ := strings.Cut(s.active, "/")
	return provider
}

func (s *chatProviderModelStub) ModelsForProvider(provider string) []string {
	var models []string
	for _, model := range s.Models() {
		if strings.HasPrefix(model, provider+"/") {
			models = append(models, model)
		}
	}
	return models
}

func (*chatProviderModelStub) SetActiveProvider(context.Context, string) error { return nil }

type chatUpdateStub struct {
	snapshot      releaseupdate.Snapshot
	deferred      releaseupdate.Snapshot
	installed     bool
	installedMeta releaseupdate.InstallMeta
}

type webDangerousStub struct {
	rollbackCalls []int
	stopCalls     []string
}

func (s *webDangerousStub) StopProcess(_ context.Context, spec string) error {
	s.stopCalls = append(s.stopCalls, spec)
	return nil
}

func (s *webDangerousStub) Rollback(_ context.Context, checkpoint int) (string, error) {
	s.rollbackCalls = append(s.rollbackCalls, checkpoint)
	return "rollback complete", nil
}

func (*webDangerousStub) ListCheckpoints(context.Context) ([]gateway.CheckpointInfo, error) {
	return []gateway.CheckpointInfo{{Number: 3, Label: "after tests"}}, nil
}

func (s *chatUpdateStub) Check(context.Context, int64) (releaseupdate.Snapshot, error) {
	return s.snapshot, nil
}

func (s *chatUpdateStub) Defer(_ context.Context, _ int64, snapshot releaseupdate.Snapshot) error {
	s.deferred = snapshot
	return nil
}

func (s *chatUpdateStub) Install(_ context.Context, _ releaseupdate.Snapshot, meta releaseupdate.InstallMeta, progress func(string)) (releaseupdate.Result, error) {
	s.installed = true
	s.installedMeta = meta
	progress("install started")
	return releaseupdate.Result{}, nil
}
func (*chatUpdateStub) CanInstall() bool { return true }

func TestChatSessionAndMessageEndpoints(t *testing.T) {
	t.Parallel()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	router.InitSessions(sessions)
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions}}
	session := gateway.SessionContext{
		SessionID: "session-1",
		Source:    gateway.SessionSource{Platform: "web", ChannelID: "browser-1"},
		CreatedAt: time.Now().UTC(), LastActiveAt: time.Now().UTC(),
	}
	if err := sessions.Save(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SaveMessage(context.Background(), session.SessionID, gateway.Message{From: "web", Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	t.Run("sessions", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", res.Code, res.Body)
		}
		var got struct {
			Sessions []gateway.SessionContext `json:"sessions"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Sessions) != 1 || got.Sessions[0].SessionID != session.SessionID {
			t.Fatalf("sessions = %#v", got.Sessions)
		}
	})

	t.Run("messages", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions/session-1/messages", nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", res.Code, res.Body)
		}
		var got []gateway.Message
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Text != "hello" {
			t.Fatalf("messages = %#v", got)
		}
	})

	t.Run("shared router command", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/message", bytes.NewBufferString(`{"channel_id":"browser-1","text":"/status"}`))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", res.Code, res.Body)
		}
		var got struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Reply == "" {
			t.Fatal("shared router returned an empty status reply")
		}
	})
}

func TestChatSessionsExposeActiveSelectorState(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	router.InitSessions(sessions)
	models := &chatProviderModelStub{active: "openrouter/sonnet"}
	router.Models = models
	server := &Server{Chat: &ChatService{
		Router:   router,
		Models:   models,
		Sessions: sessions,
		Personas: gateway.NewPersonaRegistry(gateway.DefaultPersonas()),
	}}
	session := gateway.SessionContext{
		SessionID: "selector-session",
		Source:    gateway.SessionSource{Platform: "web", ChannelID: "selector-browser"},
	}
	if err := sessions.Save(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if !server.Chat.Personas.SetActive(session.SessionID, "concise") {
		t.Fatal("set active persona failed")
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	var got struct {
		ActiveModel    string              `json:"active_model"`
		ActiveProvider string              `json:"active_provider"`
		Providers      []string            `json:"providers"`
		Models         map[string][]string `json:"models_by_provider"`
		Personas       map[string]string   `json:"active_personas"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ActiveModel != "openrouter/sonnet" || got.ActiveProvider != "openrouter" {
		t.Fatalf("active model state = %#v", got)
	}
	if !slices.Equal(got.Providers, []string{"openai", "openrouter"}) || !slices.Equal(got.Models["openrouter"], []string{"openrouter/sonnet"}) {
		t.Fatalf("provider model state = %#v / %#v", got.Providers, got.Models)
	}
	if got.Personas[session.SessionID] != "concise" {
		t.Fatalf("active personas = %#v", got.Personas)
	}
}

func TestChatUpdateEndpoints(t *testing.T) {
	updates := &chatUpdateStub{snapshot: releaseupdate.Snapshot{Components: []releaseupdate.Component{{ID: "gateway", Label: "Gateway", Installed: "1.0", Available: "1.1"}}}}
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions, Updates: updates}}

	get := httptest.NewRecorder()
	server.Handler().ServeHTTP(get, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/update", nil))
	if get.Code != http.StatusOK || !bytes.Contains(get.Body.Bytes(), []byte(`"can_install":true`)) {
		t.Fatalf("update status = %d, body = %s", get.Code, get.Body.String())
	}

	body, _ := json.Marshal(chatUpdateRequest{Snapshot: updates.snapshot})
	deferRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deferRes, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/update/defer", bytes.NewReader(body)))
	if deferRes.Code != http.StatusOK || len(updates.deferred.Components) != 1 {
		t.Fatalf("update defer = %d, body = %s", deferRes.Code, deferRes.Body.String())
	}

	installBody, _ := json.Marshal(chatUpdateRequest{Snapshot: updates.snapshot})
	installRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(installRes, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/update/install", bytes.NewReader(installBody)))
	if installRes.Code != http.StatusOK || !updates.installed {
		t.Fatalf("update install = %d, body = %s", installRes.Code, installRes.Body.String())
	}

	updates.installed = false
	displayed := releaseupdate.Snapshot{Components: append([]releaseupdate.Component(nil), updates.snapshot.Components...)}
	updates.snapshot.Components[0].Available = "1.2"
	staleBody, _ := json.Marshal(chatUpdateRequest{Snapshot: displayed})
	staleRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(staleRes, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/update/install", bytes.NewReader(staleBody)))
	if staleRes.Code != http.StatusConflict || updates.installed {
		t.Fatalf("stale update install = %d, body = %s, installed = %v", staleRes.Code, staleRes.Body.String(), updates.installed)
	}
}

func TestChatUpdateEndpointRejectsTypedNilService(t *testing.T) {
	var updates *releaseupdate.Service
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions, Updates: updates}}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/update", nil))
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("typed-nil update service status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestChatDangerousApprovalEndpoints(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	authority := &webDangerousStub{}
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions, Dangerous: NewDangerousService(authority)}}

	state := httptest.NewRecorder()
	server.Handler().ServeHTTP(state, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/dangerous", nil))
	if state.Code != http.StatusOK || !bytes.Contains(state.Body.Bytes(), []byte(`"Number":3`)) {
		t.Fatalf("dangerous state = %d, body = %s", state.Code, state.Body.String())
	}

	request := httptest.NewRecorder()
	server.Handler().ServeHTTP(request, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/dangerous/rollback", bytes.NewBufferString(`{"spec":"3"}`)))
	if request.Code != http.StatusOK || len(authority.rollbackCalls) != 0 {
		t.Fatalf("rollback request = %d, body = %s, calls=%v", request.Code, request.Body.String(), authority.rollbackCalls)
	}
	var pending struct {
		Action DangerousActionView `json:"action"`
	}
	if err := json.Unmarshal(request.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}

	decisionBody := bytes.NewBufferString(`{"decision":"approve"}`)
	decision := httptest.NewRecorder()
	server.Handler().ServeHTTP(decision, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/dangerous/"+pending.Action.ID+"/decision", decisionBody))
	if decision.Code != http.StatusOK || len(authority.rollbackCalls) != 1 || authority.rollbackCalls[0] != 3 {
		t.Fatalf("rollback decision = %d, body = %s, calls=%v", decision.Code, decision.Body.String(), authority.rollbackCalls)
	}
}

func TestChatCommandCatalogIncludesEnabledCapabilities(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	server := &Server{Chat: &ChatService{
		Router: router, Sessions: sessions,
		Updates: &chatUpdateStub{}, Dangerous: NewDangerousService(&webDangerousStub{}),
	}}
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("catalog status = %d, body = %s", res.Code, res.Body.String())
	}
	var body struct {
		Commands []gateway.CommandSpec `json:"commands"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, command := range body.Commands {
		found[command.Command] = true
	}
	for _, command := range []string{"/update", "/rollback", "/stop"} {
		if !found[command] {
			t.Errorf("command catalog missing enabled %s", command)
		}
	}
}

func TestChatCancelEndpointStopsSessionTurn(t *testing.T) {
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	router.InitSessions(sessions)
	turns := gateway.NewTurns(slog.Default())
	server := &Server{Chat: &ChatService{Router: router, Sessions: sessions, Turns: turns}}
	if err := sessions.Save(context.Background(), gateway.SessionContext{
		SessionID: "session-1", Source: gateway.SessionSource{Platform: "web", ChannelID: "browser-1"},
	}); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	stopped := make(chan struct{})
	turns.Submit(context.Background(), "session-1", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	})
	<-started

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/cancel", strings.NewReader(`{"session_id":"session-1"}`))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("cancel endpoint did not stop the turn")
	}
	var body struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Cancelled {
		t.Fatal("cancel endpoint reported no cancelled turn")
	}
}

// chatTestServer builds the fixture the chat endpoint tests share: a web
// gateway router on an in-memory session store.
func chatTestServer(t *testing.T) (*Server, gateway.SessionStore) {
	t.Helper()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })
	router := gateway.NewRouter(chatStatusStub{}, nil, "web")
	router.InitSessions(sessions)
	return &Server{Chat: &ChatService{Router: router, Sessions: sessions}}, sessions
}

func saveWebSession(t *testing.T, sessions gateway.SessionStore, id string) {
	t.Helper()
	if err := sessions.Save(context.Background(), gateway.SessionContext{
		SessionID:    id,
		Source:       gateway.SessionSource{Platform: "web", ChannelID: "browser-" + id},
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestChatEndpointsRequireToken proves the whole chat surface sits behind the
// same token gate as the rest of the dashboard, not a parallel auth story.
func TestChatEndpointsRequireToken(t *testing.T) {
	server := &Server{Token: "secret"}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil)
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a tokenless request", res.Code)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code == http.StatusUnauthorized {
		t.Fatal("valid bearer token was rejected")
	}
	if res.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501 (chat unconfigured) past the token gate", res.Code)
	}
}

// TestChatEndpointsUnavailableWithoutChatService proves every chat route
// degrades to 501 rather than panicking when the daemon runs without chat.
func TestChatEndpointsUnavailableWithoutChatService(t *testing.T) {
	server := &Server{}
	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/chat/sessions", ""},
		{http.MethodGet, "/api/chat/sessions/s-1/messages", ""},
		{http.MethodPost, "/api/chat/message", `{"channel_id":"c","text":"hi"}`},
		{http.MethodPost, "/api/chat/stream", `{"channel_id":"c","text":"hi"}`},
		{http.MethodPost, "/api/chat/persona", `{"session_id":"s","name":"concise"}`},
		{http.MethodPost, "/api/chat/update/install", ""},
	}
	for _, tc := range cases {
		var body io.Reader
		if tc.body != "" {
			body = strings.NewReader(tc.body)
		}
		req := httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, body)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusNotImplemented {
			t.Errorf("%s %s = %d, want 501", tc.method, tc.path, res.Code)
		}
	}
}

// TestChatMessageDecodeErrors proves malformed or incomplete chat messages are
// refused at the boundary, before any router state is touched.
func TestChatMessageDecodeErrors(t *testing.T) {
	server, _ := chatTestServer(t)
	cases := []struct {
		name, body string
	}{
		{"malformed json", `{`},
		{"blank text", `{"channel_id":"c","text":"   "}`},
		{"missing channel_id", `{"text":"hi"}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/message", strings.NewReader(tc.body))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, res.Code)
		}
	}
}

// TestChatSessionsFiltersNonWeb proves the dashboard only lists and serves
// browser-originated sessions: a Telegram session is invisible to it and its
// transcript is not reachable by guessing the id.
func TestChatSessionsFiltersNonWeb(t *testing.T) {
	server, sessions := chatTestServer(t)
	saveWebSession(t, sessions, "web-1")
	if err := sessions.Save(context.Background(), gateway.SessionContext{
		SessionID:    "tg-1",
		Source:       gateway.SessionSource{Platform: "telegram", ChannelID: "42"},
		CreatedAt:    time.Now().UTC(),
		LastActiveAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("sessions status = %d, body = %s", res.Code, res.Body)
	}
	var got struct {
		Sessions []gateway.SessionContext `json:"sessions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].SessionID != "web-1" {
		t.Fatalf("sessions = %#v, want only the web session", got.Sessions)
	}

	res = httptest.NewRecorder()
	server.Handler().ServeHTTP(res, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/chat/sessions/tg-1/messages", nil))
	if res.Code != http.StatusNotFound {
		t.Fatalf("telegram transcript status = %d, want 404", res.Code)
	}
}

// chatSSEEvent is one parsed `data: {...}` frame from the stream endpoint.
type chatSSEEvent struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	Tool       string `json:"tool"`
	ToolCallID string `json:"tool_call_id"`
	Parameters string `json:"parameters"`
	Failed     bool   `json:"failed"`
	SessionID  string `json:"session_id"`
	Path       string `json:"path"`
	Label      string `json:"label"`
}

func parseChatSSE(t *testing.T, body string) []chatSSEEvent {
	t.Helper()
	var events []chatSSEEvent
	for frame := range strings.SplitSeq(body, "\n\n") {
		line := strings.TrimSpace(frame)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			t.Fatalf("malformed SSE frame: %q", frame)
		}
		var ev chatSSEEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("invalid SSE payload %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

// TestChatStreamEndsWithDone proves the stream endpoint answers a local
// command through the shared router and frames the reply as a single done
// event carrying the session the turn ran in.
func TestChatStreamEndsWithDone(t *testing.T) {
	server, _ := chatTestServer(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-1","text":"/status"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	events := parseChatSSE(t, res.Body.String())
	if len(events) == 0 || events[len(events)-1].Type != "done" {
		t.Fatalf("events = %#v, want a final done event", events)
	}
	last := events[len(events)-1]
	if last.Text == "" {
		t.Fatal("done event carried an empty reply")
	}
	if last.SessionID == "" {
		t.Fatal("done event carried no session_id")
	}
}

// TestChatStreamDeltas proves a real streaming responder's deltas are emitted
// as SSE delta events before the final done event.
func TestChatStreamDeltas(t *testing.T) {
	server, sessions := chatTestServer(t)
	var gotDeltas []string
	server.Chat.Router.LLMStream = func(_ context.Context, _ gateway.Message, stream gateway.TurnStream) (string, error) {
		stream.Delta("part one")
		stream.Delta("part two")
		return "full reply", nil
	}
	saveWebSession(t, sessions, "web-1")
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-web-1","text":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	events := parseChatSSE(t, res.Body.String())
	for _, ev := range events {
		if ev.Type == "delta" {
			gotDeltas = append(gotDeltas, ev.Text)
		}
	}
	if len(gotDeltas) != 2 || gotDeltas[0] != "part one" || gotDeltas[1] != "part two" {
		t.Fatalf("deltas = %#v", gotDeltas)
	}
	if events[len(events)-1].Type != "done" || events[len(events)-1].Text != "full reply" {
		t.Fatalf("final event = %#v, want done/full reply", events[len(events)-1])
	}
}

// Tool activity has to reach the browser interleaved with the text that
// surrounds it, on both stream paths: without a turn queue the responder
// writes straight to the SSE writer, with one it hands events across a
// goroutine, and an ordering that survives only the first is not a fix.
func TestChatStreamReportsToolCalls(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		turn.Delta("checking")
		turn.ToolCall(gateway.ToolCallEvent{ID: "call-1", Name: "shell", Parameters: `{"cmd":"true"}`, Output: "exit 0\nignored trailing line"})
		turn.ToolCall(gateway.ToolCallEvent{Name: "read", Err: "no such file"})
		// A successful tool whose own output happens to start with
		// "failed:" (a grep hit, a log line read back) must be reported
		// as not failed: Failed is driven by Err, never by Text content.
		turn.ToolCall(gateway.ToolCallEvent{Name: "grep", Output: "failed: no such file\nmatch"})
		turn.Delta(" and answering")
		return "checking and answering", nil
	}

	tests := []struct {
		name   string
		queued bool
	}{
		{name: "direct"},
		{name: "queued", queued: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, sessions := chatTestServer(t)
			server.Chat.Router.LLMStream = stream
			// Enabled explicitly: this test exercises the tool-narration
			// path itself, which is off by default (see
			// TestChatStreamHidesToolCallsWhenShowToolCallsIsOff).
			server.Cfg = config.NewHolder(config.Config{Chat: config.ChatConfig{ShowToolCalls: true}})
			if tc.queued {
				server.Chat.Turns = gateway.NewTurns(slog.Default())
			}
			saveWebSession(t, sessions, "web-1")

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
				strings.NewReader(`{"channel_id":"browser-web-1","text":"hello"}`))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body)
			}

			var got []string
			for _, ev := range parseChatSSE(t, res.Body.String()) {
				switch ev.Type {
				case "delta":
					got = append(got, "text:"+ev.Text)
				case "tool":
					got = append(got, fmt.Sprintf("tool:%s:%s:%s:%s:failed=%v", ev.ToolCallID, ev.Tool, ev.Parameters, ev.Text, ev.Failed))
				}
			}
			want := []string{
				"text:checking",
				`tool:call-1:shell:{"cmd":"true"}:exit 0:failed=false`,
				"tool::read::failed: no such file:failed=true",
				"tool::grep::failed: no such file:failed=false",
				"text: and answering",
			}
			if len(got) != len(want) {
				t.Fatalf("stream events = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("stream events = %v, want %v", got, want)
				}
			}
		})
	}
}

// The dashboard has no inline media rendering path yet (see
// archie-core-1786748942243-6-f109697e), so a MediaEvent has to degrade to
// a link in the text stream rather than being silently dropped -- the same
// fallback Telegram's liveReply uses when SendMedia itself fails.
func TestChatStreamRendersMediaAsALinkFallback(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		turn.Delta("here you go")
		turn.Media(gateway.MediaEvent{
			ToolName:   "video_gen",
			Attachment: gateway.MediaAttachment{Type: "video", URL: "https://example.com/v.mp4"},
		})
		return "here you go", nil
	}

	server, sessions := chatTestServer(t)
	server.Chat.Router.LLMStream = stream
	saveWebSession(t, sessions, "web-1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-web-1","text":"make me a video"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}

	var joined strings.Builder
	for _, ev := range parseChatSSE(t, res.Body.String()) {
		if ev.Type == "delta" {
			joined.WriteString(ev.Text)
		}
	}
	if !strings.Contains(joined.String(), "https://example.com/v.mp4") {
		t.Fatalf("delta stream = %q, want it to contain the asset URL", joined.String())
	}
}

// An event with no URL has nothing to link to, so it must not add an
// empty or broken-looking line to the transcript.
func TestChatStreamSkipsMediaWithNoURL(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		turn.Media(gateway.MediaEvent{ToolName: "video_gen", Attachment: gateway.MediaAttachment{Type: "video"}})
		return "", nil
	}

	server, sessions := chatTestServer(t)
	server.Chat.Router.LLMStream = stream
	saveWebSession(t, sessions, "web-1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-web-1","text":"make me a video"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}

	for _, ev := range parseChatSSE(t, res.Body.String()) {
		if ev.Type == "delta" && ev.Text != "" {
			t.Fatalf("got a delta for a URL-less media event: %q", ev.Text)
		}
	}
}

// config.ChatConfig.ShowToolCalls is off by default and shared by every chat
// channel: an operator who left it unset (or explicitly turned it off) must
// see no tool activity on the dashboard, not just in Telegram. Previously the
// web stream narrated every tool call unconditionally, because nothing here
// read this setting at all.
func TestChatStreamHidesToolCallsWhenShowToolCallsIsOff(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		turn.Delta("checking")
		turn.ToolCall(gateway.ToolCallEvent{Name: "shell", Output: "exit 0"})
		turn.Delta(" and answering")
		return "checking and answering", nil
	}

	tests := []struct {
		name string
		cfg  *config.Holder
	}{
		{name: "no Cfg wired at all", cfg: nil},
		{name: "Cfg present but show_tool_calls unset", cfg: config.NewHolder(config.Config{})},
		{name: "show_tool_calls explicitly false", cfg: config.NewHolder(config.Config{Chat: config.ChatConfig{ShowToolCalls: false}})},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, sessions := chatTestServer(t)
			server.Chat.Router.LLMStream = stream
			server.Cfg = tc.cfg
			saveWebSession(t, sessions, "web-1")

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
				strings.NewReader(`{"channel_id":"browser-web-1","text":"hello"}`))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body)
			}

			for _, ev := range parseChatSSE(t, res.Body.String()) {
				if ev.Type == "tool" {
					t.Fatalf("got a tool frame with show_tool_calls off: %+v", ev)
				}
			}
		})
	}
}

// TestChatPersonaEndpoint proves the persona surface: switching to a known
// persona works, an unknown name is refused, a non-web session is invisible,
// and an unconfigured registry degrades to 501.
func TestChatPersonaEndpoint(t *testing.T) {
	server, sessions := chatTestServer(t)
	server.Chat.Personas = gateway.NewPersonaRegistry(gateway.DefaultPersonas())
	saveWebSession(t, sessions, "web-1")

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/persona", strings.NewReader(body))
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		return res
	}

	if res := post(`{"session_id":"web-1","name":"concise"}`); res.Code != http.StatusOK {
		t.Fatalf("known persona: status = %d, body = %s", res.Code, res.Body)
	}
	if res := post(`{"session_id":"web-1","name":"does-not-exist"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown persona: status = %d, body = %s", res.Code, res.Body)
	}
	if res := post(`{"session_id":"tg-1","name":"concise"}`); res.Code != http.StatusNotFound {
		t.Fatalf("non-web session: status = %d, body = %s", res.Code, res.Body)
	}
	if res := post(`{"session_id":"","name":"concise"}`); res.Code != http.StatusBadRequest {
		t.Fatalf("blank session: status = %d, body = %s", res.Code, res.Body)
	}

	nilServer, nilSessions := chatTestServer(t)
	saveWebSession(t, nilSessions, "web-1")
	if res := postTo(nilServer, `{"session_id":"web-1","name":"concise"}`); res.Code != http.StatusNotImplemented {
		t.Fatalf("nil personas: status = %d, body = %s", res.Code, res.Body)
	}
}

func postTo(server *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/persona", strings.NewReader(body))
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	return res
}

// The dashboard cannot upload a file at all, so a local attachment must
// say so in the transcript. Skipping it silently is the same defect
// send_file was built to end: the model reports a file as sent and
// nothing arrives, with no signal either way.
func TestChatStreamReportsUndeliverableLocalFile(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		turn.Media(gateway.MediaEvent{
			ToolName: "send_file",
			Attachment: gateway.MediaAttachment{
				Type:     "document",
				Path:     "/var/log/archie/session.log",
				FileName: "session.log",
			},
		})
		return "", nil
	}

	server, sessions := chatTestServer(t)
	server.Chat.Router.LLMStream = stream
	saveWebSession(t, sessions, "web-1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-web-1","text":"send me the log"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}

	var joined strings.Builder
	for _, ev := range parseChatSSE(t, res.Body.String()) {
		if ev.Type == "delta" {
			joined.WriteString(ev.Text)
		}
	}
	if !strings.Contains(joined.String(), "session.log") {
		t.Errorf("delta stream = %q, want it to name the undelivered file", joined.String())
	}
	if !strings.Contains(joined.String(), "could not") {
		t.Errorf("delta stream = %q, want it to report non-delivery", joined.String())
	}
}

// TestChatStreamCarriesCurrentPage proves the web chat decodes the operator's
// dashboard page from the request body and hands it to the agent as
// Message.Page, so the system prompt can state where the operator is.
func TestChatStreamCarriesCurrentPage(t *testing.T) {
	server, sessions := chatTestServer(t)
	var got gateway.Message
	server.Chat.Router.LLMStream = func(_ context.Context, msg gateway.Message, stream gateway.TurnStream) (string, error) {
		got = msg
		stream.Delta("on it")
		return "on it", nil
	}
	saveWebSession(t, sessions, "web-1")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
		strings.NewReader(`{"channel_id":"browser-web-1","text":"where do I see that?","page":"/tasks"}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	server.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body)
	}
	if got.Page != "/tasks" {
		t.Fatalf("Message.Page = %q, want /tasks", got.Page)
	}
}

// TestChatStreamEmitsNavigateChip proves a dashboard_navigate tool call emits
// a dedicated navigate frame (with the resolved path/label) even when
// ShowToolCalls is off, so the browser can render a clickable chip to route
// the operator to the page.
func TestChatStreamEmitsNavigateChip(t *testing.T) {
	stream := func(_ context.Context, _ gateway.Message, turn gateway.TurnStream) (string, error) {
		out, _ := json.Marshal(gateway.DashboardNavigateResult{Path: "/tasks", Label: "Tasks"})
		turn.ToolCall(gateway.ToolCallEvent{ID: "nav-1", Name: "dashboard_navigate", Output: string(out)})
		return "That is on the Tasks page.", nil
	}

	tests := []struct {
		name string
		cfg  *config.Holder
	}{
		{name: "show_tool_calls off (navigate is unconditional)", cfg: config.NewHolder(config.Config{Chat: config.ChatConfig{ShowToolCalls: false}})},
		{name: "no Cfg wired at all", cfg: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, sessions := chatTestServer(t)
			server.Chat.Router.LLMStream = stream
			server.Cfg = tc.cfg
			saveWebSession(t, sessions, "web-1")

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/chat/stream",
				strings.NewReader(`{"channel_id":"browser-web-1","text":"show me tasks"}`))
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			server.Handler().ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", res.Code, res.Body)
			}
			var chipFound bool
			for _, ev := range parseChatSSE(t, res.Body.String()) {
				if ev.Type == "navigate" && ev.Path == "/tasks" && ev.Label == "Tasks" {
					chipFound = true
				} else if ev.Type == "tool" {
					t.Fatalf("navigate tool leaked as a tool frame: %+v", ev)
				}
			}
			if !chipFound {
				t.Fatalf("no navigate chip emitted; frames = %#v", parseChatSSE(t, res.Body.String()))
			}
		})
	}
}
