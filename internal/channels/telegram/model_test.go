package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/gateway"
)

type modelManagerStub struct {
	models []string
	active string
}

func (m *modelManagerStub) Models() []string {
	return append([]string(nil), m.models...)
}

func (m *modelManagerStub) ActiveModel() string {
	return m.active
}

func (m *modelManagerStub) SetActiveModel(_ context.Context, ref string) error {
	if slices.Contains(m.models, ref) {
		m.active = ref
		return nil
	}
	return fmt.Errorf("unknown model: %s", ref)
}

func (m *modelManagerStub) Providers() []string {
	seen := make(map[string]bool)
	var providers []string
	for _, model := range m.models {
		provider, _, ok := strings.Cut(model, "/")
		if ok && !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	return providers
}

func (m *modelManagerStub) ActiveProvider() string {
	provider, _, _ := strings.Cut(m.active, "/")
	return provider
}

func (m *modelManagerStub) ModelsForProvider(provider string) []string {
	var models []string
	for _, model := range m.models {
		if strings.HasPrefix(model, provider+"/") {
			models = append(models, model)
		}
	}
	return models
}

func (m *modelManagerStub) SetActiveProvider(_ context.Context, provider string) error {
	models := m.ModelsForProvider(provider)
	if len(models) == 0 {
		return fmt.Errorf("unknown provider: %s", provider)
	}
	m.active = models[0]
	return nil
}

type telegramRequest struct {
	method string
	form   map[string]string
}

func newTelegramTestBot(t *testing.T) (*bot.Bot, *[]telegramRequest) {
	t.Helper()

	var requests []telegramRequest
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse Telegram request: %v", err)
		}
		form := make(map[string]string, len(r.MultipartForm.Value))
		for key, values := range r.MultipartForm.Value {
			if len(values) > 0 {
				form[key] = values[0]
			}
		}
		requests = append(requests, telegramRequest{
			method: r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:],
			form:   form,
		})

		w.Header().Set("Content-Type", "application/json")
		switch requests[len(requests)-1].method {
		case "answerCallbackQuery", "sendChatAction":
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":9,"date":1,"chat":{"id":7,"type":"private"}}}`))
		}
	}))
	t.Cleanup(api.Close)

	b, err := bot.New(
		"1:test",
		bot.WithServerURL(api.URL),
		bot.WithSkipGetMe(),
		bot.WithNotAsyncHandlers(),
	)
	if err != nil {
		t.Fatalf("new test bot: %v", err)
	}
	return b, &requests
}

func TestModelCommandShowsInlineSelector(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{"provider/alpha", "provider/beta"},
		active: "provider/beta",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.defaultHandler(router)(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/model",
		},
	})

	var markup models.InlineKeyboardMarkup
	found := false
	for _, request := range *requests {
		raw, ok := request.form["reply_markup"]
		if request.method != "sendMessage" || !ok {
			continue
		}
		if err := json.Unmarshal([]byte(raw), &markup); err != nil {
			t.Fatalf("decode reply markup: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatalf("/model did not send an inline keyboard; requests: %#v", *requests)
	}
	if got := modelSelectorText("deepseek/deepseek-v4-pro"); got != "Select a model:\nCurrent Provider: DeepSeek\nActive: deepseek-v4-pro" {
		t.Errorf("model selector text = %q", got)
	}
	if got := len(markup.InlineKeyboard); got != len(manager.models) {
		t.Fatalf("keyboard rows = %d, want %d", got, len(manager.models))
	}
	for index, row := range markup.InlineKeyboard {
		if len(row) != 1 {
			t.Fatalf("keyboard row %d has %d buttons, want 1", index, len(row))
		}
		button := row[0]
		if !strings.HasPrefix(button.CallbackData, modelCallbackPrefix) || button.CallbackData == fmt.Sprintf("model:%d", index) {
			t.Errorf("button %d callback = %q, want a stable model token", index, button.CallbackData)
		}
		_, modelLabel, _ := strings.Cut(manager.models[index], "/")
		if !strings.Contains(button.Text, modelLabel) {
			t.Errorf("button %d text = %q, want model label %q", index, button.Text, modelLabel)
		}
	}
	if !strings.HasPrefix(markup.InlineKeyboard[1][0].Text, "✓ ") {
		t.Errorf("active model button is not marked: %q", markup.InlineKeyboard[1][0].Text)
	}
	for _, row := range markup.InlineKeyboard {
		if strings.Contains(row[0].Text, "provider/") {
			t.Errorf("model button must not repeat the provider: %q", row[0].Text)
		}
	}
}

func TestProviderCommandFiltersSubsequentModelSelector(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{
			"openrouter/openai/gpt-5.6",
			"openai/gpt-5.6",
			"openai/o3",
		},
		active: "openrouter/openai/gpt-5.6",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.defaultHandler(router)(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/provider",
		},
	})
	var providerMarkup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &providerMarkup); err != nil {
		t.Fatal(err)
	}
	if len(providerMarkup.InlineKeyboard) != 2 {
		t.Fatalf("provider rows = %d, want 2", len(providerMarkup.InlineKeyboard))
	}

	*requests = nil
	providerCallback := providerMarkup.InlineKeyboard[1][0].CallbackData
	g.defaultHandler(router)(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "provider-callback",
			From: models.User{ID: allowedUserID},
			Data: providerCallback,
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})
	if manager.ActiveProvider() != "openai" {
		t.Fatalf("active provider = %q, want openai", manager.ActiveProvider())
	}

	*requests = nil
	g.defaultHandler(router)(context.Background(), b, &models.Update{
		Message: &models.Message{
			From: &models.User{ID: allowedUserID},
			Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
			Text: "/model",
		},
	})
	var modelMarkup models.InlineKeyboardMarkup
	for _, request := range *requests {
		if raw := request.form["reply_markup"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &modelMarkup); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(modelMarkup.InlineKeyboard) != 2 {
		t.Fatalf("filtered model rows = %d, want 2", len(modelMarkup.InlineKeyboard))
	}
	for _, row := range modelMarkup.InlineKeyboard {
		if strings.Contains(row[0].Text, "openrouter") {
			t.Fatalf("OpenRouter model leaked into OpenAI selection: %q", row[0].Text)
		}
	}
}

func TestProviderCallbackHonoursTheProviderRenderedInTheSelector(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{"openai/gpt-5.6", "openrouter/openai/gpt-5.6"},
		active: "openai/gpt-5.6",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, _ := newTelegramTestBot(t)

	markup := g.providerSelectorKeyboard(manager)
	renderedCallback := markup.InlineKeyboard[0][0].CallbackData
	manager.models = []string{"deepseek/deepseek-v4-pro", "openai/gpt-5.6", "openrouter/openai/gpt-5.6"}

	g.handleProviderCallback(context.Background(), b, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "callback-id",
		From: models.User{ID: allowedUserID},
		Data: renderedCallback,
	}}, router)

	if got := manager.ActiveProvider(); got != "openai" {
		t.Errorf("stale selector chose %q, want the originally rendered provider", got)
	}
}

func TestModelCallbackSwitchesAndUpdatesSelector(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{"provider/alpha", "provider/beta"},
		active: "provider/alpha",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.defaultHandler(router)(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID:   "callback-id",
			From: models.User{ID: allowedUserID},
			Data: g.modelCallbackToken("provider/beta"),
			Message: models.MaybeInaccessibleMessage{
				Type:    models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{ID: 9, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}},
			},
		},
	})

	if manager.active != "provider/beta" {
		t.Fatalf("active model = %q, want provider/beta", manager.active)
	}
	var answered, edited bool
	for _, request := range *requests {
		if request.method == "answerCallbackQuery" {
			answered = true
			if got := request.form["text"]; got != "The model has been changed to: beta" {
				t.Errorf("selection acknowledgement = %q", got)
			}
		}
		edited = edited || request.method == "editMessageText"
	}
	if !answered || !edited {
		t.Fatalf("callback answered=%t edited=%t, requests: %#v", answered, edited, *requests)
	}
}

func TestModelCallbackHonoursTheModelRenderedInTheSelector(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{"openai/gpt-5.6", "openrouter/openai/gpt-5.6"},
		active: "openai/gpt-5.6",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, _ := newTelegramTestBot(t)

	markup := g.modelSelectorKeyboard(manager)
	renderedCallback := markup.InlineKeyboard[0][0].CallbackData
	if err := manager.SetActiveProvider(context.Background(), "openrouter"); err != nil {
		t.Fatal(err)
	}

	g.handleModelCallback(context.Background(), b, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID:   "callback-id",
		From: models.User{ID: allowedUserID},
		Data: renderedCallback,
	}}, router)

	if got := manager.ActiveModel(); got != "openai/gpt-5.6" {
		t.Errorf("stale selector chose %q, want the originally rendered model", got)
	}
}

func TestModelCallbackRejectsUnauthorizedAndMalformedSelections(t *testing.T) {
	const allowedUserID = int64(42)
	tests := []struct {
		name   string
		userID int64
		data   string
	}{
		{name: "unauthorized sender", userID: 99, data: "model:1"},
		{name: "malformed index", userID: allowedUserID, data: "model:not-an-index"},
		{name: "out of range", userID: allowedUserID, data: "model:99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &modelManagerStub{
				models: []string{"provider/alpha", "provider/beta"},
				active: "provider/alpha",
			}
			router := gateway.NewRouter(nil, nil, "telegram")
			router.Models = manager
			g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
			b, requests := newTelegramTestBot(t)

			g.defaultHandler(router)(context.Background(), b, &models.Update{
				CallbackQuery: &models.CallbackQuery{
					ID:   "callback-id",
					From: models.User{ID: tt.userID},
					Data: tt.data,
				},
			})

			if manager.active != "provider/alpha" {
				t.Fatalf("active model changed to %q", manager.active)
			}
			var answer *telegramRequest
			for index := range *requests {
				if (*requests)[index].method == "answerCallbackQuery" {
					answer = &(*requests)[index]
				}
			}
			if answer == nil || answer.form["show_alert"] != "true" {
				t.Fatalf("rejection did not answer with an alert; requests: %#v", *requests)
			}
		})
	}
}

func TestProviderCallbackRejectsUnauthorizedAndMalformedSelections(t *testing.T) {
	const allowedUserID = int64(42)
	tests := []struct {
		name   string
		userID int64
		data   string
	}{
		{name: "unauthorized sender", userID: 99, data: "provider:1"},
		{name: "malformed index", userID: allowedUserID, data: "provider:not-an-index"},
		{name: "out of range", userID: allowedUserID, data: "provider:99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &modelManagerStub{
				models: []string{"openrouter/openai/gpt-5.6", "openai/gpt-5.6"},
				active: "openrouter/openai/gpt-5.6",
			}
			router := gateway.NewRouter(nil, nil, "telegram")
			router.Models = manager
			g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
			b, requests := newTelegramTestBot(t)

			g.defaultHandler(router)(context.Background(), b, &models.Update{
				CallbackQuery: &models.CallbackQuery{
					ID:   "callback-id",
					From: models.User{ID: tt.userID},
					Data: tt.data,
				},
			})

			if manager.active != "openrouter/openai/gpt-5.6" {
				t.Fatalf("active provider/model changed to %q", manager.active)
			}
			var answer *telegramRequest
			for index := range *requests {
				if (*requests)[index].method == "answerCallbackQuery" {
					answer = &(*requests)[index]
				}
			}
			if answer == nil || answer.form["show_alert"] != "true" {
				t.Fatalf("rejection did not answer with an alert; requests: %#v", *requests)
			}
		})
	}
}

// TestParseModelRestReturnsDeclaredValuesInOrder covers the naked return that a
// nakedret autofix expanded in 6d4e0be. Every result shares a type with at
// least one other -- two strings, three bools -- so a transposed pair compiles
// silently and would surface only as /model switching the wrong scope. Each
// case sets exactly one flag so no pair of bools can swap undetected.
func TestParseModelRestReturnsDeclaredValuesInOrder(t *testing.T) {
	tests := []struct {
		name     string
		rest     string
		model    string
		provider string
		global   bool
		session  bool
		refresh  bool
	}{
		{name: "model only", rest: "gpt-5.6", model: "gpt-5.6"},
		{name: "inline provider", rest: "gpt-5.6 --provider=openai", model: "gpt-5.6", provider: "openai"},
		{name: "separated provider", rest: "gpt-5.6 --provider openai", model: "gpt-5.6", provider: "openai"},
		{name: "global scope", rest: "gpt-5.6 --global", model: "gpt-5.6", global: true},
		{name: "session scope", rest: "gpt-5.6 --session", model: "gpt-5.6", session: true},
		{name: "refresh", rest: "--refresh", refresh: true},
		{name: "provider without model", rest: "--provider=openai --global", provider: "openai", global: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, provider, global, session, refresh := parseModelRest(tt.rest)
			if model != tt.model || provider != tt.provider {
				t.Fatalf("parseModelRest(%q) = model %q, provider %q; want %q, %q",
					tt.rest, model, provider, tt.model, tt.provider)
			}
			if global != tt.global || session != tt.session || refresh != tt.refresh {
				t.Fatalf("parseModelRest(%q) = global %v, session %v, refresh %v; want %v, %v, %v",
					tt.rest, global, session, refresh, tt.global, tt.session, tt.refresh)
			}
		})
	}
}

// TestParseModelRestDoesNotDoubleTheModelName guards the regression introduced
// by the nestif extraction in 651de0a: the extracted helper appended surviving
// fields to a slice the caller then appended to again, so "/model gpt-5.6"
// asked the router for "gpt-5.6 gpt-5.6" and every direct model switch failed
// as an unknown model. A separated provider value was doubled into the name
// too. The assertion is on field count rather than equality so that a future
// re-extraction cannot pass by doubling a different part of the string.
func TestParseModelRestDoesNotDoubleTheModelName(t *testing.T) {
	for _, rest := range []string{
		"gpt-5.6",
		"gpt-5.6 --global",
		"gpt-5.6 --provider openai",
		"gpt-5.6 --provider=openai --session",
	} {
		model, _, _, _, _ := parseModelRest(rest)
		if fields := strings.Fields(model); len(fields) != 1 {
			t.Fatalf("parseModelRest(%q) model = %q; want a single field", rest, model)
		}
	}
}
