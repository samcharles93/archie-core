package gateway

import (
	"context"
	"testing"
)

// mediaStubAdapter implements BasePlatformAdapter, MediaSender, and
// CapabilityReporter, reporting Media: true.
type mediaStubAdapter struct {
	stubAdapter
}

func (s *mediaStubAdapter) SendMedia(ctx context.Context, e MessageEvent) (SendResult, error) {
	return SendResult{Success: true, MessageID: "media-1"}, nil
}

func (s *mediaStubAdapter) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Media: true}
}

var (
	_ BasePlatformAdapter = (*mediaStubAdapter)(nil)
	_ MediaSender         = (*mediaStubAdapter)(nil)
	_ CapabilityReporter  = (*mediaStubAdapter)(nil)
)

func TestCapabilitiesOf_MatchesImplementedInterfaces(t *testing.T) {
	tests := []struct {
		name       string
		adapter    any
		wantMedia  bool
		implsMedia bool
	}{
		{
			name:       "adapter without media support",
			adapter:    &stubAdapter{},
			wantMedia:  false,
			implsMedia: false,
		},
		{
			name:       "adapter with media support",
			adapter:    &mediaStubAdapter{},
			wantMedia:  true,
			implsMedia: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := CapabilitiesOf(tt.adapter)
			if caps.Media != tt.wantMedia {
				t.Errorf("CapabilitiesOf(%s).Media = %v, want %v", tt.name, caps.Media, tt.wantMedia)
			}

			_, implementsMediaSender := tt.adapter.(MediaSender)
			if implementsMediaSender != tt.implsMedia {
				t.Errorf("%s: MediaSender type assertion = %v, want %v", tt.name, implementsMediaSender, tt.implsMedia)
			}

			// The regression the acceptance criteria calls for: a claimed
			// capability must match the actually-implemented interface.
			if caps.Media != implementsMediaSender {
				t.Errorf("%s: Capabilities().Media (%v) disagrees with MediaSender implementation (%v)", tt.name, caps.Media, implementsMediaSender)
			}
		})
	}
}

func TestMediaSender_SendMedia(t *testing.T) {
	a := &mediaStubAdapter{}
	sr, err := a.SendMedia(context.Background(), MessageEvent{
		Type:  MsgVideo,
		Media: []MediaAttachment{{Type: "video", URL: "https://example.com/v.mp4"}},
	})
	if err != nil {
		t.Fatalf("SendMedia: %v", err)
	}
	if !sr.Success {
		t.Error("expected SendMedia to succeed")
	}
}
