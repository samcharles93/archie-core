package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

func TestUpdateShowsComponentSectionsAndActions(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.Updates = &updateStub{snapshot: releaseupdate.Snapshot{Components: []releaseupdate.Component{
		{ID: "gateway", Label: "THE GATEWAY", Installed: "v0.1.0", Available: "v0.1.1", Changelog: "- Clearer help"},
		{ID: "runtime", Label: "THE RUNTIME", Installed: "v0.1.0"},
	}}}
	b, requests := newTelegramTestBot(t)

	g.updateHandler()(context.Background(), b, &models.Update{Message: &models.Message{
		From: &models.User{ID: 42}, Chat: models.Chat{ID: 7, Type: models.ChatTypePrivate}, Text: "/update",
	}})

	if len(*requests) != 1 || (*requests)[0].method != "sendMessage" {
		t.Fatalf("requests = %#v, want update message", *requests)
	}
	text := (*requests)[0].form["text"]
	for _, want := range []string{"Archie has an update available:", "--- THE GATEWAY ---", "v0.1.1 available", "- Clearer help", "--- THE RUNTIME ---", "No runtime changes"} {
		if !strings.Contains(text, want) {
			t.Errorf("update text missing %q: %q", want, text)
		}
	}
	if !strings.Contains((*requests)[0].form["reply_markup"], updateApproveCallback) || !strings.Contains((*requests)[0].form["reply_markup"], updateDeferCallback) {
		t.Errorf("update keyboard = %q", (*requests)[0].form["reply_markup"])
	}
}

func TestUpdateCallbackRejectsUnauthorizedSender(t *testing.T) {
	g := New("1:test", "", "", []int64{42}, slog.Default())
	g.Updates = &updateStub{}
	b, requests := newTelegramTestBot(t)

	g.handleUpdateCallback(context.Background(), b, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "callback", From: models.User{ID: 99}, Data: updateApproveCallback,
	}})

	stub, ok := g.Updates.(*updateStub)
	if !ok {
		t.Fatalf("g.Updates is %T, want *updateStub", g.Updates)
	}
	if stub.installCalls != 0 {
		t.Error("unauthorized callback started an installation")
	}
	if len(*requests) != 1 || (*requests)[0].method != "answerCallbackQuery" {
		t.Fatalf("requests = %#v, want authorization response", *requests)
	}
}

type updateStub struct {
	snapshot     releaseupdate.Snapshot
	installCalls int
}

func (s *updateStub) Check(context.Context, int64) (releaseupdate.Snapshot, error) {
	return s.snapshot, nil
}
func (s *updateStub) Defer(context.Context, int64, releaseupdate.Snapshot) error { return nil }
func (s *updateStub) Install(_ context.Context, progress func(string)) error {
	s.installCalls++
	progress("Update started.")
	return nil
}
func (*updateStub) CanInstall() bool { return true }
