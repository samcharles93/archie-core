package gateway

import (
	"encoding/json"
	"testing"
)

func TestMessageTypeIsMedia(t *testing.T) {
	tests := []struct {
		typ     MessageType
		isMedia bool
	}{
		{MsgText, false},
		{MsgImage, true},
		{MsgVideo, true},
		{MsgAudio, true},
		{MsgDocument, true},
		{MsgLocation, false},
		{MsgSticker, true},
		{MsgContact, false},
		{MsgCallback, false},
		{MsgReaction, false},
		{MsgEdited, false},
		{MsgDeleted, false},
		{MsgSystem, false},
	}
	for _, tc := range tests {
		got := tc.typ.IsMedia()
		if got != tc.isMedia {
			t.Errorf("%s.IsMedia() = %v, want %v", tc.typ, got, tc.isMedia)
		}
	}
}

func TestMessageTypeIsEphemeral(t *testing.T) {
	ephemeral := map[MessageType]bool{
		MsgReaction: true,
		MsgDeleted:  true,
	}
	for _, typ := range []MessageType{
		MsgText, MsgImage, MsgVideo, MsgAudio, MsgDocument,
		MsgLocation, MsgSticker, MsgContact, MsgCallback, MsgSystem,
	} {
		if typ.IsEphemeral() {
			t.Errorf("%s should not be ephemeral", typ)
		}
	}
	for typ, want := range ephemeral {
		if got := typ.IsEphemeral(); got != want {
			t.Errorf("%s.IsEphemeral() = %v, want %v", typ, got, want)
		}
	}
}

func TestMessageTypeIsRich(t *testing.T) {
	if MsgText.IsRich() {
		t.Error("text should not be rich")
	}
	rich := []MessageType{MsgImage, MsgVideo, MsgAudio, MsgDocument, MsgLocation, MsgSticker, MsgContact, MsgCallback, MsgReaction, MsgEdited, MsgDeleted, MsgSystem}
	for _, typ := range rich {
		if !typ.IsRich() {
			t.Errorf("%s should be rich", typ)
		}
	}
}

func TestMessageTypeRoundTripJSON(t *testing.T) {
	e := MessageEvent{Type: MsgImage, Text: "alt text", ChannelID: "c1", Platform: "telegram", SenderID: "u1"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded MessageEvent
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != MsgImage {
		t.Errorf("Type = %q", decoded.Type)
	}
}
