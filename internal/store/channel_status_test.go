package store

import (
	"testing"
	"time"
)

// TestChannelStatusReplacesTheReportedSet: the process hosting the channels owns
// the set it reports, so a channel it stops reporting must disappear. Leaving it
// behind would show a stale "running" for a channel that is gone, which is worse
// than showing nothing.
func TestChannelStatusReplacesTheReportedSet(t *testing.T) {
	st := openTest(t)
	ctx := t.Context()

	if rows, err := st.ChannelStatus(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("ChannelStatus on an empty store = (%+v, %v), want (none, nil)", rows, err)
	}

	observed := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	first := []ChannelStatus{
		{ID: "telegram", Name: "telegram", State: "running", Configured: true, ReloadSupported: true, ObservedAt: observed},
		{ID: "webhook", Name: "webhook", State: "failed", Detail: "listen: address in use", Configured: true, ObservedAt: observed},
	}
	if err := st.PutChannelStatus(ctx, first); err != nil {
		t.Fatalf("PutChannelStatus: %v", err)
	}
	rows, err := st.ChannelStatus(ctx)
	if err != nil {
		t.Fatalf("ChannelStatus: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %+v, want both channels", rows)
	}
	// Ordered by id, so a reader and this test see the same list twice.
	if rows[0].ID != "telegram" || rows[1].ID != "webhook" {
		t.Errorf("rows = %+v, want telegram then webhook", rows)
	}
	if rows[1].Detail != "listen: address in use" || rows[1].State != "failed" {
		t.Errorf("webhook row = %+v, want the failure it reported", rows[1])
	}
	if !rows[0].ReloadSupported || !rows[0].Configured {
		t.Errorf("telegram row = %+v, want its declared capabilities", rows[0])
	}
	if !rows[0].ObservedAt.Equal(observed) {
		t.Errorf("ObservedAt = %v, want %v", rows[0].ObservedAt, observed)
	}

	// The reporter no longer mentions webhook: its row goes.
	if err := st.PutChannelStatus(ctx, first[:1]); err != nil {
		t.Fatalf("PutChannelStatus: %v", err)
	}
	rows, err = st.ChannelStatus(ctx)
	if err != nil {
		t.Fatalf("ChannelStatus: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "telegram" {
		t.Fatalf("rows = %+v, want only telegram after the shorter report", rows)
	}

	// An empty report is a report: a process that hosts no channels clears the set
	// rather than leaving the previous one standing.
	if err := st.PutChannelStatus(ctx, nil); err != nil {
		t.Fatalf("PutChannelStatus(empty): %v", err)
	}
	if rows, err := st.ChannelStatus(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("rows after an empty report = (%+v, %v), want none", rows, err)
	}
}

// TestChannelStatusRequiresAnID: a row without an id cannot be replaced or
// removed by a later report, so it is refused at the producer rather than
// inserted as an orphan the reporter can never clear.
func TestChannelStatusRequiresAnID(t *testing.T) {
	st := openTest(t)
	if err := st.PutChannelStatus(t.Context(), []ChannelStatus{{State: "running"}}); err == nil {
		t.Fatal("PutChannelStatus with no id = nil, want a refusal")
	}
}
