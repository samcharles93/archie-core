package nats

import "testing"

func TestEndpointKey(t *testing.T) {
	tests := []struct {
		name    string
		service string
		id      string
		want    string
	}{
		{name: "simple", service: "curator", id: "inst-1", want: "curator.inst-1"},
		{name: "single-char id", service: "scheduler", id: "a", want: "scheduler.a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := endpointKey(tt.service, tt.id); got != tt.want {
				t.Errorf("endpointKey(%q, %q) = %q, want %q", tt.service, tt.id, got, tt.want)
			}
		})
	}
}

func TestEndpointKeyPrefix(t *testing.T) {
	if got := endpointKeyPrefix("curator"); got != "curator.*" {
		t.Errorf("endpointKeyPrefix(curator) = %q, want %q", got, "curator.*")
	}
}

func TestIDFromEndpointKey(t *testing.T) {
	tests := []struct {
		name    string
		service string
		key     string
		want    string
	}{
		{name: "own key returns id", service: "curator", key: "curator.inst-1", want: "inst-1"},
		{name: "other service", service: "curator", key: "scheduler.inst", want: ""},
		{name: "empty id", service: "curator", key: "curator.", want: ""},
		{name: "key equals prefix", service: "curator", key: "curator", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := idFromEndpointKey(tt.service, tt.key); got != tt.want {
				t.Errorf("idFromEndpointKey(%q, %q) = %q, want %q", tt.service, tt.key, got, tt.want)
			}
		})
	}
}
