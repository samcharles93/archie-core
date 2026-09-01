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

type detailedModelManagerStub struct {
	*modelManagerStub
	details map[string]gateway.ModelDetails
}

func (m *detailedModelManagerStub) ModelDetails(ref string) (gateway.ModelDetails, bool) {
	details, ok := m.details[ref]
	return details, ok
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
		models: []string{"provider/alpha", "provider/beta", "other/gamma"},
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
	if got := len(markup.InlineKeyboard); got != 2 {
		t.Fatalf("provider keyboard rows = %d, want provider row and cancel row", got)
	}
	if got := len(markup.InlineKeyboard[0]); got != 2 {
		t.Fatalf("provider buttons = %d, want 2", got)
	}
	if !strings.Contains(markup.InlineKeyboard[0][0].Text, "(2)") {
		t.Errorf("provider button lacks model count: %q", markup.InlineKeyboard[0][0].Text)
	}
	if !strings.HasPrefix(markup.InlineKeyboard[0][0].Text, "✓ ") {
		t.Errorf("active provider button is not marked: %q", markup.InlineKeyboard[0][0].Text)
	}
	if got := markup.InlineKeyboard[1][0].CallbackData; got != modelCancelCallback {
		t.Errorf("provider cancel callback = %q", got)
	}
}

func TestModelCommandDrillsFromProviderIntoFilteredModels(t *testing.T) {
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
			Text: "/model",
		},
	})
	var providerMarkup models.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte((*requests)[0].form["reply_markup"]), &providerMarkup); err != nil {
		t.Fatal(err)
	}
	if len(providerMarkup.InlineKeyboard) != 2 || len(providerMarkup.InlineKeyboard[0]) != 2 {
		t.Fatalf("provider grid = %#v, want two-column row and cancel", providerMarkup.InlineKeyboard)
	}

	*requests = nil
	providerCallback := providerMarkup.InlineKeyboard[0][1].CallbackData
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
	if manager.ActiveProvider() != "openrouter" {
		t.Fatalf("provider selection changed active model prematurely: %q", manager.ActiveProvider())
	}

	var modelMarkup models.InlineKeyboardMarkup
	for _, request := range *requests {
		if raw := request.form["reply_markup"]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &modelMarkup); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(modelMarkup.InlineKeyboard) != 2 {
		t.Fatalf("filtered model rows = %d, want one two-column model row plus navigation", len(modelMarkup.InlineKeyboard))
	}
	for _, button := range modelMarkup.InlineKeyboard[0] {
		model, ok := g.modelForCallback(button.CallbackData)
		if !ok {
			continue
		}
		if strings.HasPrefix(model, "openrouter/") {
			t.Fatalf("OpenRouter model leaked into OpenAI selection: %q", model)
		}
	}
}

// TestModelSelectorKeyboardPageAcrossModelCounts covers the sizes that
// actually stress pageBounds and the nav-row logic: none, one (no pairing,
// no nav), exactly one page size (no nav), one over a page (nav appears),
// and enough to need many pages (nav survives past double digits).
func TestModelSelectorKeyboardPageAcrossModelCounts(t *testing.T) {
	tests := []struct {
		name      string
		count     int
		wantPages int
	}{
		{"zero models", 0, 1},
		{"one model", 1, 1},
		{"exactly one page", modelPageSize, 1},
		{"one over a page", modelPageSize + 1, 2},
		{"many pages", 100, (100 + modelPageSize - 1) / modelPageSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := &modelManagerStub{}
			for i := 0; i < tt.count; i++ {
				manager.models = append(manager.models, fmt.Sprintf("provider/model-%02d", i))
			}
			g := New("1:test", "", "", []int64{42}, slog.Default())

			page := g.modelSelectorKeyboardPage(manager, "provider", 0)
			rows := page.InlineKeyboard
			if len(rows) == 0 {
				t.Fatalf("no rows returned for %d models", tt.count)
			}

			actions := rows[len(rows)-1]
			if len(actions) != 2 || actions[0].CallbackData != modelBackCallback ||
				actions[1].CallbackData != modelCancelCallback {
				t.Fatalf("actions row = %#v, want back+cancel", actions)
			}

			modelButtons := 0
			for _, row := range rows[:len(rows)-1] {
				for _, button := range row {
					if button.CallbackData == modelNoopCallback {
						continue
					}
					if _, isPageLink := g.modelPageForCallback(button.CallbackData); isPageLink {
						continue
					}
					modelButtons++
				}
			}
			if modelButtons != min(tt.count, modelPageSize) {
				t.Fatalf("model buttons on first page = %d, want %d", modelButtons, min(tt.count, modelPageSize))
			}

			hasNav := tt.wantPages > 1
			navRowPresent := false
			for _, row := range rows[:len(rows)-1] {
				for _, button := range row {
					if button.CallbackData == modelNoopCallback {
						navRowPresent = true
					}
				}
			}
			if navRowPresent != hasNav {
				t.Fatalf("nav row present = %v, want %v for %d models", navRowPresent, hasNav, tt.count)
			}
		})
	}
}

func TestModelSelectorPageCallbackAdvancesToNextPage(t *testing.T) {
	manager := &modelManagerStub{active: "provider/model-09"}
	for i := 1; i <= 9; i++ {
		manager.models = append(manager.models, fmt.Sprintf("provider/model-%02d", i))
	}
	g := New("1:test", "", "", []int64{42}, slog.Default())

	first := g.modelSelectorKeyboardPage(manager, "provider", 0)
	nav := first.InlineKeyboard[4]
	next, ok := g.modelPageForCallback(nav[1].CallbackData)
	if !ok {
		t.Fatal("next page callback was not recorded")
	}

	second := g.modelSelectorKeyboardPage(manager, next.Provider, next.Page)
	if got := len(second.InlineKeyboard[0]); got != 1 {
		t.Fatalf("second page model row = %d buttons, want 1 (nine models, page size eight)", got)
	}
	if got := second.InlineKeyboard[0][0].Text; got != "✓ model-09" {
		t.Fatalf("second page model = %q", got)
	}
}

func TestModelSelectionConfirmationIncludesCatalogMetadata(t *testing.T) {
	const ref = "google/gemini-3.6-flash"
	manager := &detailedModelManagerStub{
		modelManagerStub: &modelManagerStub{models: []string{ref}, active: ref},
		details: map[string]gateway.ModelDetails{
			ref: {
				Ref: ref, ContextWindow: 1_048_576, MaxOutputTokens: 65_536,
				Reasoning: true, Tools: true, Structured: true,
				InputModalities: []string{"text", "image", "audio"},
			},
		},
	}

	got := modelSelectionConfirmation(manager, ref)
	for _, want := range []string{
		"Model switched to gemini-3.6-flash",
		"Provider: Google",
		"Context: 1048576 tokens",
		"Max output: 65536 tokens",
		"Capabilities: reasoning, tools, image, audio, structured output",
		"active for this daemon process; resets on restart",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("confirmation missing %q:\n%s", want, got)
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
		t.Errorf("opening a provider page changed active provider to %q", got)
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
		if request.method == "editMessageText" {
			edited = true
			if markup, present := request.form["reply_markup"]; present {
				t.Errorf("confirmation edit sent reply_markup=%q, want field omitted", markup)
			}
		}
	}
	if !answered || !edited {
		t.Fatalf("callback answered=%t edited=%t, requests: %#v", answered, edited, *requests)
	}
}

func TestDirectModelSelectionWithProviderIsAtomicOnFailure(t *testing.T) {
	const allowedUserID = int64(42)
	manager := &modelManagerStub{
		models: []string{"openai/gpt-5.6", "anthropic/claude"},
		active: "openai/gpt-5.6",
	}
	router := gateway.NewRouter(nil, nil, "telegram")
	router.Models = manager
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, _ := newTelegramTestBot(t)

	g.handleModelCommand(context.Background(), b, &models.Message{
		From: &models.User{ID: allowedUserID},
		Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate},
		Text: "/model missing --provider anthropic",
	}, router)

	if manager.active != "openai/gpt-5.6" {
		t.Fatalf("failed selection changed active model to %q", manager.active)
	}
}

func TestModelPageIndicatorCallbackIsAnswered(t *testing.T) {
	const allowedUserID = int64(42)
	g := New("1:test", "", "", []int64{allowedUserID}, slog.Default())
	b, requests := newTelegramTestBot(t)

	g.defaultHandler(gateway.NewRouter(nil, nil, "telegram"))(context.Background(), b, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			ID: "noop-id", From: models.User{ID: allowedUserID}, Data: modelNoopCallback,
		},
	})

	if len(*requests) != 1 || (*requests)[0].method != "answerCallbackQuery" {
		t.Fatalf("page indicator requests = %#v, want callback acknowledgement", *requests)
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
