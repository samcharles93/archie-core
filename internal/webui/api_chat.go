package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

// ChatService is the shared conversational surface exposed to the dashboard.
// It deliberately contains the gateway Router rather than duplicating command
// dispatch in the HTTP layer.
type ChatService struct {
	Router           *gateway.Router
	Sessions         gateway.SessionStore
	Turns            *gateway.Turns
	Models           gateway.ModelManager
	Personas         *gateway.PersonaRegistry
	Updates          ChatUpdateService
	Dangerous        *DangerousService
	updateMu         sync.Mutex
	updateInProgress bool
}

// ChatUpdateService is the shared release workflow used by chat gateways.
// The web adapter uses recipient zero for the locally authenticated operator.
type ChatUpdateService interface {
	Check(context.Context, int64) (releaseupdate.Snapshot, error)
	Defer(context.Context, int64, releaseupdate.Snapshot) error
	Install(context.Context, func(string)) error
	CanInstall() bool
}

type chatMessageRequest struct {
	ChannelID string `json:"channel_id"`
	SourceID  string `json:"source_id"`
	Text      string `json:"text"`
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

type dangerousRequest struct {
	Spec string `json:"spec"`
}

type dangerousDecisionRequest struct {
	Decision string `json:"decision"`
}

func (s *Server) chatReady(w http.ResponseWriter) (*ChatService, bool) {
	if s.Chat == nil || s.Chat.Router == nil || s.Chat.Sessions == nil {
		http.Error(w, "chat is not configured", http.StatusNotImplemented)
		return nil, false
	}
	if !chatUpdateServiceConfigured(s.Chat.Router.Updates) && chatUpdateServiceConfigured(s.Chat.Updates) {
		s.Chat.Router.Updates = s.Chat.Updates
	}
	if s.Chat.Router.Dangerous == nil && s.Chat.Dangerous != nil {
		s.Chat.Router.Dangerous = s.Chat.Dangerous
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
	sessions, err := chat.Sessions.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]gateway.SessionContext, 0, len(sessions))
	for _, session := range sessions {
		if session.Source.Platform == "web" {
			out = append(out, session)
		}
	}
	writeJSON(w, map[string]any{
		"sessions":            out,
		"models":              modelNames(chat.Models),
		"models_by_provider":  modelsByProvider(chat.Models),
		"providers":           modelProviders(chat.Models),
		"active_model":        activeModel(chat.Models),
		"active_provider":     activeProvider(chat.Models),
		"personas":            personaNames(chat.Personas),
		"active_personas":     activePersonas(chat.Personas, out),
		"commands":            chatCommandSpecs(chat),
		"restart_available":   chat.Router.Restart != nil,
		"dangerous_available": chat.Dangerous != nil,
	})
}

func chatCommandSpecs(chat *ChatService) []gateway.CommandSpec {
	specs := gateway.LocalCommandSpecs()
	if chat.Dangerous != nil {
		specs = append(specs,
			gateway.CommandSpec{Command: "/rollback", Description: "Request approval to restore a filesystem checkpoint", Usage: "/rollback [number]"},
			gateway.CommandSpec{Command: "/stop", Description: "Request approval to terminate a background process", Usage: "/stop <process-name>"},
			gateway.CommandSpec{Command: "/deny", Description: "Deny a pending dangerous action", Usage: "/deny <action-id>"},
		)
	}
	return specs
}

func (s *Server) handleChatMessages(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	sessionID := r.PathValue("id")
	session, err := chat.Sessions.Get(r.Context(), sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if session == nil || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	messages, err := chat.Sessions.RecentMessages(r.Context(), sessionID, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, messages)
}

func (s *Server) decodeChatMessage(w http.ResponseWriter, r *http.Request) (gateway.Message, bool) {
	var req chatMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid chat message", http.StatusBadRequest)
		return gateway.Message{}, false
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || req.ChannelID == "" {
		http.Error(w, "text and channel_id are required", http.StatusBadRequest)
		return gateway.Message{}, false
	}
	if req.SourceID == "" {
		req.SourceID = newChatSourceID()
	}
	return gateway.Message{
		SourceID: req.SourceID, ChannelID: req.ChannelID,
		ThreadID: "", From: "web", Text: req.Text,
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
	reply, err := chat.Router.Route(r.Context(), msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionID, err := chat.Router.ResolveSessionKey(r.Context(), msg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"reply": reply, "session_id": sessionID})
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
	writeChatEvent := func(kind, text, sessionID string) {
		payload, _ := json.Marshal(map[string]string{"type": kind, "text": text, "session_id": sessionID})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
	sessionID, err := chat.Router.ResolveSessionKey(r.Context(), msg)
	if err != nil {
		writeChatEvent("error", err.Error(), "")
		return
	}
	writeChatEvent("started", "", sessionID)
	var reply string
	if chat.Turns == nil {
		reply, err = chat.Router.RouteStream(r.Context(), msg, func(delta string) {
			writeChatEvent("delta", delta, sessionID)
		})
	} else {
		type turnResult struct {
			reply string
			err   error
		}
		events := make(chan string, 32)
		result := make(chan turnResult, 1)
		chat.Turns.Submit(r.Context(), sessionID, func(turnCtx context.Context) {
			turnReply, turnErr := chat.Router.RouteStream(turnCtx, msg, func(delta string) {
				select {
				case events <- delta:
				case <-r.Context().Done():
				}
			})
			result <- turnResult{reply: turnReply, err: turnErr}
		})
		for {
			select {
			case delta := <-events:
				writeChatEvent("delta", delta, sessionID)
			case turn := <-result:
				for {
					select {
					case delta := <-events:
						writeChatEvent("delta", delta, sessionID)
					default:
						goto drained
					}
				}
			drained:
				reply, err = turn.reply, turn.err
				goto turnDone
			case <-r.Context().Done():
				chat.Turns.Stop(sessionID)
				return
			}
		}
	turnDone:
	}
	if err != nil {
		writeChatEvent("error", err.Error(), sessionID)
		return
	}
	if resolved, resolveErr := chat.Router.ResolveSessionKey(r.Context(), msg); resolveErr == nil {
		sessionID = resolved
	}
	writeChatEvent("done", reply, sessionID)
}

func (s *Server) handleChatCancel(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if chat.Turns == nil {
		http.Error(w, "chat cancellation is not configured", http.StatusNotImplemented)
		return
	}
	var req chatCancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	session, err := chat.Sessions.Get(r.Context(), req.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if session == nil || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	cancelled, dropped := chat.Turns.Stop(req.SessionID)
	writeJSON(w, map[string]any{"cancelled": cancelled, "dropped": dropped})
}

func (s *Server) handleChatPersona(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if chat.Personas == nil {
		http.Error(w, "personality switching is not configured", http.StatusNotImplemented)
		return
	}
	var req chatPersonaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" || req.Name == "" {
		http.Error(w, "session_id and name are required", http.StatusBadRequest)
		return
	}
	if session, err := chat.Sessions.Get(r.Context(), req.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if session == nil || session.Source.Platform != "web" {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if !chat.Personas.SetActive(req.SessionID, strings.ToLower(req.Name)) {
		http.Error(w, fmt.Sprintf("unknown personality %q", req.Name), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "name": strings.ToLower(req.Name)})
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
		http.Error(w, err.Error(), http.StatusBadGateway)
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
		http.Error(w, err.Error(), http.StatusBadGateway)
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
	err = chat.Updates.Install(r.Context(), func(message string) { progress = append(progress, message) })
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "progress": progress})
}

func (s *Server) handleChatDangerousState(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if chat.Dangerous == nil {
		http.Error(w, "dangerous sandbox actions are not configured", http.StatusNotImplemented)
		return
	}
	checkpoints, err := chat.Dangerous.Checkpoints(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"checkpoints": checkpoints, "pending": chat.Dangerous.Pending()})
}

func (s *Server) handleChatDangerousRequest(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if chat.Dangerous == nil {
		http.Error(w, "dangerous sandbox actions are not configured", http.StatusNotImplemented)
		return
	}
	var req dangerousRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Spec) == "" {
		http.Error(w, "spec is required", http.StatusBadRequest)
		return
	}
	action, result, executed, err := chat.Dangerous.Request(r.Context(), r.PathValue("kind"), req.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"action": action, "result": result, "executed": executed})
}

func (s *Server) handleChatDangerousDecision(w http.ResponseWriter, r *http.Request) {
	chat, ok := s.chatReady(w)
	if !ok {
		return
	}
	if chat.Dangerous == nil {
		http.Error(w, "dangerous sandbox actions are not configured", http.StatusNotImplemented)
		return
	}
	var req dangerousDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Decision) == "" {
		http.Error(w, "decision is required", http.StatusBadRequest)
		return
	}
	result, err := chat.Dangerous.Decide(r.Context(), r.PathValue("id"), req.Decision)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "result": result})
}

func modelNames(models gateway.ModelManager) []string {
	if models == nil {
		return []string{}
	}
	return models.Models()
}

func activeModel(models gateway.ModelManager) string {
	if models == nil {
		return ""
	}
	return models.ActiveModel()
}

func activeProvider(models gateway.ModelManager) string {
	manager, ok := models.(gateway.ProviderModelManager)
	if !ok {
		return ""
	}
	return manager.ActiveProvider()
}

func modelProviders(models gateway.ModelManager) []string {
	manager, ok := models.(gateway.ProviderModelManager)
	if !ok {
		return []string{}
	}
	return manager.Providers()
}

func modelsByProvider(models gateway.ModelManager) map[string][]string {
	if models == nil {
		return map[string][]string{}
	}
	if manager, ok := models.(gateway.ProviderModelManager); ok {
		groups := make(map[string][]string, len(manager.Providers()))
		for _, provider := range manager.Providers() {
			groups[provider] = manager.ModelsForProvider(provider)
		}
		return groups
	}
	groups := make(map[string][]string)
	for _, model := range models.Models() {
		provider, _, ok := strings.Cut(model, "/")
		if !ok {
			provider = ""
		}
		groups[provider] = append(groups[provider], model)
	}
	return groups
}

func activePersonas(personas *gateway.PersonaRegistry, sessions []gateway.SessionContext) map[string]string {
	active := make(map[string]string, len(sessions))
	if personas == nil {
		return active
	}
	for _, session := range sessions {
		active[session.SessionID] = personas.ActiveName(session.SessionID)
	}
	return active
}

func personaNames(personas *gateway.PersonaRegistry) []string {
	if personas == nil {
		return []string{}
	}
	return personas.List()
}

func newChatSourceID() string {
	return "web-" + uuid.NewString()
}
