package store

import (
	"testing"
	"time"
)

// TestConfigSnapshotRoundTrip: the daemon publishes the dashboard's
// configuration projection here and the UI process reads it, so the document
// has to come back byte-identical and the newest publish has to win. There is
// one running configuration, so there is one row.
func TestConfigSnapshotRoundTrip(t *testing.T) {
	st := openTest(t)
	ctx := t.Context()

	if _, found, err := st.ConfigSnapshot(ctx); err != nil || found {
		t.Fatalf("ConfigSnapshot on an empty store = (found %v, %v), want (false, nil)", found, err)
	}

	first := ConfigSnapshot{
		Schema:      "webui.ConfigView/1",
		Document:    []byte(`{"identity":{"bot_user":"archie"}}`),
		PublishedAt: time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC),
	}
	if err := st.PutConfigSnapshot(ctx, first); err != nil {
		t.Fatalf("PutConfigSnapshot: %v", err)
	}

	got, found, err := st.ConfigSnapshot(ctx)
	if err != nil || !found {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want it stored", found, err)
	}
	if got.Schema != first.Schema || string(got.Document) != string(first.Document) {
		t.Fatalf("snapshot = %+v, want %+v", got, first)
	}
	if !got.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("published at = %v, want %v", got.PublishedAt, first.PublishedAt)
	}

	second := ConfigSnapshot{
		Schema:      "webui.ConfigView/1",
		Document:    []byte(`{"identity":{"bot_user":"archie-2"}}`),
		PublishedAt: first.PublishedAt.Add(time.Hour),
	}
	if err := st.PutConfigSnapshot(ctx, second); err != nil {
		t.Fatalf("republish: %v", err)
	}
	got, found, err = st.ConfigSnapshot(ctx)
	if err != nil || !found {
		t.Fatalf("ConfigSnapshot after republish = (found %v, %v)", found, err)
	}
	if string(got.Document) != string(second.Document) {
		t.Fatalf("document = %s, want the republished %s", got.Document, second.Document)
	}

	var rows int
	if err := st.db.QueryRowContext(ctx, `SELECT count(*) FROM config_snapshot`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("config_snapshot holds %d rows, want exactly 1: republishing replaces, it does not accumulate", rows)
	}
}
