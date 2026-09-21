package webui

import (
	"context"
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

type fakeChannelStatusStore struct {
	rows []storecontract.ChannelStatus
	err  error
}

func (f fakeChannelStatusStore) PutChannelStatus(context.Context, []storecontract.ChannelStatus) error {
	return nil
}

func (f fakeChannelStatusStore) ChannelStatus(context.Context) ([]storecontract.ChannelStatus, error) {
	return f.rows, f.err
}

// TestRemoteChannelStatusMapsReportedState: the dashboard process reads channel
// state another process published, so the mapping is the whole of its
// responsibility -- including carrying an unknown state through rather than
// dropping the channel, because a reader that hides a channel it does not
// recognise shows a healthy page for a channel that is down.
func TestRemoteChannelStatusMapsReportedState(t *testing.T) {
	source := RemoteChannelStatus(fakeChannelStatusStore{rows: []storecontract.ChannelStatus{
		{ID: "telegram", Name: "telegram", State: "running", Configured: true, ReloadSupported: true},
		{ID: "webhook", Name: "webhook", State: "failed", Detail: "listen: address in use", Configured: true},
		{ID: "email", Name: "email", State: "invented-later", Configured: true},
	}})

	snapshot := source.Snapshot()
	if len(snapshot) != 3 {
		t.Fatalf("Snapshot() = %+v, want all three reported channels", snapshot)
	}
	if snapshot[0].State != status.StateRunning || !snapshot[0].ReloadSupported {
		t.Errorf("telegram = %+v, want running and reload-capable", snapshot[0])
	}
	if snapshot[1].State != status.StateFailed || snapshot[1].Detail == "" {
		t.Errorf("webhook = %+v, want the failure and its reason", snapshot[1])
	}
	if snapshot[2].State != status.State("invented-later") {
		t.Errorf("email state = %q, want the reported value carried through", snapshot[2].State)
	}
}

// TestRemoteChannelStatusReportsNothingWhenTheStoreFails: the handler has no
// error to return, and the honest answer is an empty report. Showing a cached or
// assumed state for a channel the dashboard cannot currently ask about is how a
// dead channel keeps looking alive.
func TestRemoteChannelStatusReportsNothingWhenTheStoreFails(t *testing.T) {
	source := RemoteChannelStatus(fakeChannelStatusStore{err: errors.New("state store unreachable")})
	if got := source.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot() = %+v, want nothing when the store cannot answer", got)
	}
}
