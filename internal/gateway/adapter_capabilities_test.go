package gateway

import (
	"context"
	"testing"
)

type noMediaSender struct{}

// mediaStubSender implements the two optional media-delivery contracts
// directly. It deliberately has no channel lifecycle methods: media senders
// are launch-scoped values, not platform adapters.
type mediaStubSender struct{}

func (s *mediaStubSender) SendMedia(ctx context.Context, e MessageEvent) (SendResult, error) {
	return SendResult{Success: true, MessageID: "media-1"}, nil
}

func (s *mediaStubSender) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Media: true}
}

var (
	_ MediaSender        = (*mediaStubSender)(nil)
	_ CapabilityReporter = (*mediaStubSender)(nil)
)

func TestCapabilitiesOf_MatchesImplementedInterfaces(t *testing.T) {
	tests := []struct {
		name       string
		sender     any
		wantMedia  bool
		implsMedia bool
	}{
		{
			name:       "sender without media support",
			sender:     &noMediaSender{},
			wantMedia:  false,
			implsMedia: false,
		},
		{
			name:       "sender with media support",
			sender:     &mediaStubSender{},
			wantMedia:  true,
			implsMedia: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := CapabilitiesOf(tt.sender)
			if caps.Media != tt.wantMedia {
				t.Errorf("CapabilitiesOf(%s).Media = %v, want %v", tt.name, caps.Media, tt.wantMedia)
			}

			_, implementsMediaSender := tt.sender.(MediaSender)
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
	a := &mediaStubSender{}
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
