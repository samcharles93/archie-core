package webui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// ChatService is the shared conversational surface exposed to the dashboard.
// Handlers depend on the Gateway-owned contract and never retain Gateway
// implementation objects.
type ChatService struct {
	Contract         messaging.ChatContract
	Updates          ChatUpdateService
	updateMu         sync.Mutex
	updateInProgress bool
}

// ChatUpdateService is the shared release workflow used by chat gateways.
// The web adapter uses recipient zero for the locally authenticated operator.
type ChatUpdateService interface {
	Check(context.Context, int64) (releaseupdate.Snapshot, error)
	Defer(context.Context, int64, releaseupdate.Snapshot) error
	Install(context.Context, releaseupdate.Snapshot, releaseupdate.InstallMeta, func(string)) (releaseupdate.Result, error)
	CanInstall() bool
}

type chatMessageRequest struct {
	ChannelID string `json:"channel_id"`
	SourceID  string `json:"source_id"`
	Text      string `json:"text"`
	// Page is the dashboard route the operator is on (e.g. "/tasks"). It is
	// set by the web chat so the agent's system prompt can state where the
	// operator is looking and point them somewhere relevant.
	Page string `json:"page,omitempty"`
}

// chatMessageView is one entry of a session's history as the dashboard
// reads it. The persisted record is messaging.Message; this is the shape
// the browser has always been served, kept here so the HTTP response owns
// its own field names rather than exposing a domain struct's.
type chatMessageView struct {
	MessageID string
	SourceID  string
	From      string
	Text      string
	At        time.Time
	// Tagged, alone among these fields: a transcript written before
	// attachments persisted must keep the payload it has always had, so a
	// message with no media omits the key rather than serving null.
	Media []chatMediaView `json:"media,omitempty"`
}

// chatMediaView is a stored message attachment as the dashboard reads it.
type chatMediaView struct {
	Type     string `json:"type"`
	FileName string `json:"file_name,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
	FileSize *int64 `json:"file_size,omitempty"`
	Width    *int   `json:"width,omitempty"`
	Height   *int   `json:"height,omitempty"`
	Duration *int   `json:"duration,omitempty"`
	URL      string `json:"url,omitempty"`
}

// chatMediaViews maps a message's stored attachments onto the transcript
// view, keeping the descriptive fields a reader needs to know a file was
// there.
func chatMediaViews(media []messaging.MediaAttachment) []chatMediaView {
	if len(media) == 0 {
		return nil
	}
	views := make([]chatMediaView, 0, len(media))
	for _, attachment := range media {
		views = append(views, chatMediaView{
			Type:     attachment.Type,
			FileName: attachment.FileName,
			MIMEType: attachment.MIMEType,
			FileSize: attachment.FileSize,
			Width:    attachment.Width,
			Height:   attachment.Height,
			Duration: attachment.Duration,
			URL:      attachment.URL,
		})
	}
	return views
}

type chatPersonaRequest struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

type chatCancelRequest struct {
	SessionID string `json:"session_id"`
}

type chatUpdateRequest struct {
	Snapshot releaseupdate.Snapshot `json:"snapshot"`
}

type chatTurnView struct {
	TurnID             string               `json:"turn_id"`
	AssistantMessageID string               `json:"assistant_message_id,omitempty"`
	Status             messaging.TurnStatus `json:"status"`
	Error              string               `json:"error,omitempty"`
	ToolCalls          []chatToolView       `json:"tool_calls,omitempty"`
}

type chatToolView struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Parameters string `json:"parameters,omitempty"`
	Summary    string `json:"summary"`
	Failed     bool   `json:"failed"`
}

func (s *Server) chatReady(w http.ResponseWriter) (*ChatService, bool) {
	if s.Chat == nil || s.Chat.Contract == nil {
		http.Error(w, "chat is not configured", http.StatusNotImplemented)
		return nil, false
	}
	return s.Chat, true
}

func chatUpdateServiceConfigured(updates ChatUpdateService) bool {
	if updates == nil {
		return false
	}
	value := reflect.ValueOf(updates)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !value.IsNil()
	default:
		return true
	}
}

func (s *Server) handleChatSessions(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	snapshot, err := chat.Contract.Snapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]messaging.SessionContext, 0, len(snapshot.Sessions))
	active := make(map[string]string)
	for _, session := range snapshot.Sessions {
		if session.Source.Platform == "web" {
			out = append(out, session)
			if name, ok := snapshot.ActivePersonas[session.SessionID]; ok {
				active[session.SessionID] = name
			}
		}
	}
	writeJSON(w, map[string]any{
		"sessions":           out,
		"models":             snapshot.Models,
		"models_by_provider": snapshot.ModelsByProvider,
		"providers":          snapshot.Providers,
		"active_model":       snapshot.ActiveModel,
		"active_provider":    snapshot.ActiveProvider,
		"personas":           snapshot.Personas,
		"active_personas":    active,
		"commands":           chatCommandSpecs(chat),
		"restart_available":  snapshot.RestartAvailable,
	})
}

func chatCommandSpecs(chat *ChatService) []messaging.CommandSpec {
	specs := messaging.LocalCommandSpecs()
	return specs
}

func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	sessionID := r.PathValue("id")
	session, found, err := chat.Contract.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	messages, err := chat.Contract.RecentMessages(r.Context(), sessionID, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]chatMessageView, 0, len(messages))
	for _, message := range messages {
		views = append(views, chatMessageView{
			MessageID: string(message.ID), SourceID: message.SourceID,
			From: message.Sender, Text: message.Text, At: message.At,
			Media: chatMediaViews(message.Media),
		})
	}
	writeJSON(w, views)
}

func (s *Server) handleChatTurns(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	sessionID := r.PathValue("id")
	session, found, err := chat.Contract.GetSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	turns, err := chat.Contract.RecentTurns(r.Context(), sessionID, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]chatTurnView, 0, len(turns))
	for _, turn := range turns {
		view := chatTurnView{
			TurnID: turn.TurnID, AssistantMessageID: turn.AssistantMessageID,
			Status: turn.Status, Error: turn.Error,
			ToolCalls: make([]chatToolView, 0, len(turn.ToolCalls)),
		}
		for _, tool := range turn.ToolCalls {
			view.ToolCalls = append(view.ToolCalls, chatToolView{
				ID: tool.ID, Name: tool.Name, Parameters: tool.Parameters,
				Summary: tool.Summary(), Failed: tool.Err != "",
			})
		}
		views = append(views, view)
	}
	writeJSON(w, views)
}

func (s *Server) decodeChatMessage(w http.ResponseWriter, r *http.Request) (messaging.Inbound, bool) {
	var req chatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid chat message", http.StatusBadRequest)
		return messaging.Inbound{}, false
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || req.ChannelID == "" {
		http.Error(w, "text and channel_id are required", http.StatusBadRequest)
		return messaging.Inbound{}, false
	}
	if req.SourceID == "" {
		req.SourceID = newChatSourceID()
	}
	// The browser has no threading, so the conversation is the channel
	// alone. Page is the route the operator is looking at, and stays out
	// of the record.
	return messaging.Inbound{
		Message: messaging.Message{
			SourceID:       req.SourceID,
			ConversationID: messaging.ConversationID{ChannelID: req.ChannelID},
			Sender:         "web",
			Role:           messaging.RoleUser,
			Text:           req.Text,
		},
		Page: req.Page,
		// The dashboard names the channel it carries; see the platform field's
		// note in internal/domain/messaging/inbound.go.
		Platform: "web",
	}, true
}

func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	msg, ok := s.decodeChatMessage(w, r)
	if !ok {
		return
	}
	reply, err := chat.Contract.Route(r.Context(), msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"reply": reply.Text, "session_id": reply.SessionID})
}

// chatStreamEvent is one frame of the chat stream. Text is always emitted.
type chatStreamEvent struct {
	Type       string `json:"type"`
	Text       string `json:"text"`
	Tool       string `json:"tool,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Parameters string `json:"parameters,omitempty"`
	Failed     bool   `json:"failed,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	// Path and Label carry a dashboard_navigate result so the browser can
	// render a clickable chip that routes the operator to the page. Present
	// only on a navigate event.
	Path  string `json:"path,omitempty"`
	Label string `json:"label,omitempty"`
}

// chatStreamSink writes turn text and tool activity to the browser stream.
// showToolCalls is fixed for the stream.
type chatStreamSink struct {
	write         func(chatStreamEvent)
	showToolCalls bool
}

func (s chatStreamSink) Delta(text string) {
	s.write(chatStreamEvent{Type: "delta", Text: text})
}

func (s chatStreamSink) ToolCall(event messaging.ToolCallEvent) {
	// A dashboard_navigate call is an explicit point-the-operator-there
	// result, not tool narration, so it renders a clickable chip even when
	// ShowToolCalls is off. Parse the resolved path/label from the tool's
	// JSON result and emit a dedicated navigate frame.
	if event.Name == "dashboard_navigate" && event.Err == "" {
		var result messaging.DashboardNavigateResult
		if err := json.Unmarshal([]byte(event.Output), &result); err == nil && result.Path != "" {
			s.write(chatStreamEvent{
				Type:       "navigate",
				Tool:       event.Name,
				ToolCallID: event.ID,
				Path:       result.Path,
				Label:      result.Label,
			})
			return
		}
	}
	if !s.showToolCalls || event.Name == "" {
		return
	}
	s.write(chatStreamEvent{
		Type:       "tool",
		Tool:       event.Name,
		ToolCallID: event.ID,
		Parameters: event.Parameters,
		Text:       event.Summary(),
		Failed:     event.Err != "",
	})
}

// Media is sent as a link in the text stream; a local file is reported as
// undelivered.
func (s chatStreamSink) Media(event messaging.MediaEvent) {
	att := event.Attachment
	switch {
	case att.URL != "":
		s.write(chatStreamEvent{Type: "delta", Text: "\n\n📎 " + att.Type + ": " + att.URL})
	case att.Path != "":
		name := att.FileName
		if name == "" {
			name = att.Path
		}
		s.write(chatStreamEvent{
			Type: "delta",
			Text: "\n\n📎 could not send " + name + ": this channel cannot deliver a local file. It is on the daemon host at " + att.Path + ".",
		})
	}
}

// chatShowToolCalls reports config.ChatConfig.ShowToolCalls, false when no
// config is available.
func (s *Server) chatShowToolCalls(ctx context.Context) bool {
	view, found, err := s.configSource()(ctx)
	if err != nil || !found {
		return false
	}
	return view.Chat.ShowToolCalls
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	msg, ok := s.decodeChatMessage(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	writeChatEvent := func(event chatStreamEvent, sessionID string) {
		event.SessionID = sessionID
		payload, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
	events, err := chat.Contract.Stream(r.Context(), msg)
	if err != nil {
		writeChatEvent(chatStreamEvent{Type: "error", Text: err.Error()}, "")
		return
	}
	sink := chatStreamSink{showToolCalls: s.chatShowToolCalls(r.Context())}
	for event := range events {
		sink.write = func(frame chatStreamEvent) { writeChatEvent(frame, event.SessionID) }
		switch event.Kind {
		case "tool":
			sink.ToolCall(event.Tool)
		case "media":
			sink.Media(event.Media)
		default:
			writeChatEvent(chatStreamEvent{Type: event.Kind, Text: event.Text}, event.SessionID)
		}
	}
}

func (s *Server) handleChatCancel(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	snapshot, err := chat.Contract.Snapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !snapshot.CancellationAvailable {
		http.Error(w, "chat cancellation is not configured", http.StatusNotImplemented)
		return
	}
	var req chatCancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	session, found, err := chat.Contract.GetSession(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	result, err := chat.Contract.Cancel(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"cancelled": result.Cancelled, "dropped": result.Dropped})
}

func (s *Server) handleChatPersona(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	snapshot, err := chat.Contract.Snapshot(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !snapshot.PersonasAvailable {
		http.Error(w, "personality switching is not configured", http.StatusNotImplemented)
		return
	}
	var req chatPersonaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" || req.Name == "" {
		http.Error(w, "session_id and name are required", http.StatusBadRequest)
		return
	}
	if session, found, err := chat.Contract.GetSession(r.Context(), req.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if !found || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	changed, err := chat.Contract.SetPersona(r.Context(), req.SessionID, strings.ToLower(req.Name))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !changed {
		http.Error(w, fmt.Sprintf("unknown personality %q", req.Name), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": strings.ToLower(req.Name)})
}

// writeUpdateCheckError answers an unset check command as not configured, which
// the dashboard shows as such rather than as a failure.
func writeUpdateCheckError(w http.ResponseWriter, err error) {
	if errors.Is(err, releaseupdate.ErrNotConfigured) {
		http.Error(w, "updates are not configured", http.StatusNotImplemented)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func (s *Server) handleChatUpdate(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if !chatUpdateServiceConfigured(chat.Updates) {
		http.Error(w, "updates are not configured", http.StatusNotImplemented)
		return
	}
	snapshot, err := chat.Updates.Check(r.Context(), 0)
	if err != nil {
		writeUpdateCheckError(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"snapshot":    snapshot,
		"available":   snapshot.Available(),
		"can_install": chat.Updates.CanInstall(),
	})
}

func (s *Server) handleChatUpdateDefer(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if !chatUpdateServiceConfigured(chat.Updates) {
		http.Error(w, "updates are not configured", http.StatusNotImplemented)
		return
	}
	var req chatUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid update snapshot", http.StatusBadRequest)
		return
	}
	if err := chat.Updates.Defer(r.Context(), 0, req.Snapshot); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleChatUpdateInstall(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if !chatUpdateServiceConfigured(chat.Updates) || !chat.Updates.CanInstall() {
		http.Error(w, "update installation is not configured", http.StatusNotImplemented)
		return
	}
	var req chatUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Snapshot.Available()) == 0 {
		http.Error(w, "the displayed update snapshot is required", http.StatusBadRequest)
		return
	}
	fresh, err := chat.Updates.Check(r.Context(), 0)
	if err != nil {
		writeUpdateCheckError(w, err)
		return
	}
	if !releaseupdate.SameAvailable(fresh, req.Snapshot) {
		http.Error(w, "available releases changed; check again", http.StatusConflict)
		return
	}
	chat.updateMu.Lock()
	if chat.updateInProgress {
		chat.updateMu.Unlock()
		http.Error(w, "an update is already in progress", http.StatusConflict)
		return
	}
	chat.updateInProgress = true
	chat.updateMu.Unlock()
	defer func() {
		chat.updateMu.Lock()
		chat.updateInProgress = false
		chat.updateMu.Unlock()
	}()

	progress := make([]string, 0, 4)
	meta := releaseupdate.InstallMeta{Channel: "webui", ReportPath: s.UpdateReportPath}
	if s.TelegramUpdateReportPath != "" && s.TelegramUpdateChatID != 0 {
		meta.Channel = "telegram"
		meta.ChatID = s.TelegramUpdateChatID
		meta.ReportPath = s.TelegramUpdateReportPath
	}
	result, err := chat.Updates.Install(r.Context(), fresh, meta, func(message string) { progress = append(progress, message) })
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "progress": progress, "result": result})
}

func newChatSourceID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return "web-" + uuid.NewString()
	}
	return "web-" + id.String()
}
