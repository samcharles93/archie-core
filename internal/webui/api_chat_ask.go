package webui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// chatAsk is a question a turn is blocked on, rendered as a card in the chat.
type chatAsk struct {
	ID      string          `json:"id"`
	Kind    string          `json:"kind"` // approval, clarify or picker
	Prompt  string          `json:"prompt"`
	Detail  string          `json:"detail,omitempty"`
	Choices []chatAskChoice `json:"choices,omitempty"`
}

type chatAskChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// chatAsks holds the asks waiting for the browser's answer, keyed by id.
type chatAsks struct {
	mu      sync.Mutex
	waiting map[string]chan string
}

func (a *chatAsks) open() (string, chan string) {
	var b [12]byte
	_, _ = rand.Read(b[:])
	id := hex.EncodeToString(b[:])
	ch := make(chan string, 1)
	a.mu.Lock()
	if a.waiting == nil {
		a.waiting = make(map[string]chan string)
	}
	a.waiting[id] = ch
	a.mu.Unlock()
	return id, ch
}

func (a *chatAsks) close(id string) {
	a.mu.Lock()
	delete(a.waiting, id)
	a.mu.Unlock()
}

func (a *chatAsks) answer(id, answer string) bool {
	a.mu.Lock()
	ch, ok := a.waiting[id]
	a.mu.Unlock()
	if ok {
		select {
		case ch <- answer:
		default:
		}
	}
	return ok
}

// dashboardAsker answers a turn's approvals and questions through cards in
// the chat stream, so the dashboard carries everything Telegram does.
type dashboardAsker struct {
	asks  *chatAsks
	write func(chatStreamEvent)
}

func (d dashboardAsker) ask(ctx context.Context, q chatAsk) (string, error) {
	id, ch := d.asks.open()
	defer d.asks.close(id)
	q.ID = id
	d.write(chatStreamEvent{Type: "ask", Ask: &q})
	select {
	case answer := <-ch:
		return answer, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (d dashboardAsker) RequestApproval(ctx context.Context, action, description string) (messaging.ApprovalDecision, error) {
	answer, err := d.ask(ctx, chatAsk{Kind: "approval", Prompt: action, Detail: description})
	switch {
	case err != nil:
		return messaging.ApprovalDenied, err
	case answer == "approve":
		return messaging.ApprovalApproved, nil
	case answer == "always":
		return messaging.ApprovalPermanentlyApproved, nil
	}
	return messaging.ApprovalDenied, nil
}

func (d dashboardAsker) RequestClarification(ctx context.Context, req messaging.ClarifyRequest) (string, error) {
	answer, err := d.ask(ctx, chatAsk{Kind: "clarify", Prompt: req.Question, Choices: askChoices(req.Suggestions)})
	if err != nil {
		return "", err
	}
	if answer = strings.TrimSpace(answer); answer == "" {
		return "", messaging.ErrEmptyReply
	}
	return answer, nil
}

func (d dashboardAsker) RequestChoice(ctx context.Context, req messaging.PickerRequest) (messaging.InteractiveChoice, error) {
	if len(req.Options) == 0 {
		return messaging.InteractiveChoice{}, messaging.ErrNoPickerOptions
	}
	answer, err := d.ask(ctx, chatAsk{Kind: "picker", Prompt: req.Prompt, Choices: askChoices(req.Options)})
	if err != nil {
		return messaging.InteractiveChoice{}, err
	}
	for _, option := range req.Options {
		if option.ID == answer {
			return option, nil
		}
	}
	return messaging.InteractiveChoice{}, errors.New("picker answer matches no option")
}

func askChoices(choices []messaging.InteractiveChoice) []chatAskChoice {
	out := make([]chatAskChoice, 0, len(choices))
	for _, c := range choices {
		out = append(out, chatAskChoice{ID: c.ID, Label: c.Label})
	}
	return out
}

// withDashboardAsks puts the dashboard's askers on a turn's context.
func withDashboardAsks(ctx context.Context, asker dashboardAsker) context.Context {
	ctx = messaging.WithApprovalRequester(ctx, asker)
	return messaging.WithInteractive(ctx, messaging.Interactive{Clarifier: asker, Picker: asker})
}

type chatAnswerRequest struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

// handleChatAnswer delivers the operator's answer to the ask a turn waits on.
func (s *Server) handleChatAnswer(w http.ResponseWriter, r *http.Request) {
	var req chatAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	if !s.chatAsks.answer(req.ID, req.Answer) {
		http.Error(w, "that question is no longer waiting", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"answered": true})
}
