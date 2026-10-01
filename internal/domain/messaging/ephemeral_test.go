package messaging

import (
	"context"
	"testing"
)

// deletableSender implements the delete contract and reports it.
type deletableSender struct{}

func (deletableSender) DeleteMessage(context.Context, MessageEvent) error { return nil }

func (deletableSender) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{Delete: true}
}

// misreportingSender implements DeleteMessage but denies the capability, so
// the capability -- not the method set -- is what callers must trust.
type misreportingSender struct{ deletableSender }

func (misreportingSender) Capabilities() AdapterCapabilities {
	return AdapterCapabilities{}
}

// A claimed capability must agree with the implemented interface: the Delete
// flag is the caller's way to know, ahead of a type assertion, whether a
// retraction is worth attempting, and a mismatch makes that signal lie.
func TestAdapterCapabilitiesDeleteMatchesImplementedInterface(t *testing.T) {
	tests := []struct {
		name       string
		sender     any
		want       bool
		implements bool
	}{
		{name: "sender with delete support", sender: deletableSender{}, want: true, implements: true},
		{name: "sender without delete support", sender: struct{}{}, want: false, implements: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := CapabilitiesOf(tt.sender)
			if caps.Delete != tt.want {
				t.Errorf("CapabilitiesOf(%s).Delete = %v, want %v", tt.name, caps.Delete, tt.want)
			}
			_, implements := tt.sender.(MessageDeleter)
			if implements != tt.implements {
				t.Errorf("%s: MessageDeleter implementation = %v, want %v", tt.name, implements, tt.implements)
			}
			if caps.Delete != implements {
				t.Errorf("%s: reported Delete (%v) disagrees with MessageDeleter implementation (%v)", tt.name, caps.Delete, implements)
			}
		})
	}
}

func TestDeleterOf(t *testing.T) {
	tests := []struct {
		name   string
		sender any
		want   bool
	}{
		{name: "capability and interface agree", sender: deletableSender{}, want: true},
		{name: "interface without capability", sender: misreportingSender{}, want: false},
		{name: "neither capability nor interface", sender: struct{}{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deleter, ok := DeleterOf(tt.sender)
			if ok != tt.want {
				t.Fatalf("DeleterOf(%s) ok = %v, want %v", tt.name, ok, tt.want)
			}
			if ok && deleter == nil {
				t.Fatalf("DeleterOf(%s) reported ok with a nil deleter", tt.name)
			}
			if !ok && deleter != nil {
				t.Fatalf("DeleterOf(%s) reported !ok with a non-nil deleter", tt.name)
			}
		})
	}
}
