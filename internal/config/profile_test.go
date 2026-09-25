package config

import (
	"slices"
	"strings"
	"testing"
)

func TestContainerProfileResolution(t *testing.T) {
	c := ContainerConfig{Image: "archie-agent:1", Profiles: map[string]AgentProfile{
		"net":   {Image: "archie-agent-net:1", Tools: []string{"whois"}},
		"quiet": {Tools: []string{"notes"}},
	}}
	tests := []struct {
		name, profile, wantImage, wantErr string
		wantTools                         []string
	}{
		{name: "default profile", profile: "", wantImage: "archie-agent:1"},
		{name: "named profile", profile: "net", wantImage: "archie-agent-net:1", wantTools: []string{"whois"}},
		{name: "profile without an image uses the default image", profile: "quiet", wantImage: "archie-agent:1", wantTools: []string{"notes"}},
		{name: "unconfigured profile", profile: "gone", wantErr: `"gone" is not configured`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := c.Profile(tt.profile)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Profile() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || p.Image != tt.wantImage || !slices.Equal(p.Tools, tt.wantTools) {
				t.Fatalf("Profile() = %+v, %v; want image %q tools %v", p, err, tt.wantImage, tt.wantTools)
			}
		})
	}
	bad := ContainerConfig{Profiles: map[string]AgentProfile{"x": {Tools: []string{" "}}}}
	if err := bad.ValidateProfiles(); err == nil {
		t.Error("ValidateProfiles accepted an empty tool name")
	}
}

func TestKitProfiles(t *testing.T) {
	const pinned = "docker/claude-code-kit@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := ContainerConfig{Image: "archie-agent:1", Profiles: map[string]AgentProfile{
		"claude": {Kit: []string{pinned}, Adapter: "claude-code"},
	}}
	p, err := c.Profile("claude")
	if err != nil || p.Image != "" || !p.IsKit() {
		t.Fatalf("Profile() = %+v, %v; a Kit profile must not inherit the agent image", p, err)
	}
	for name, bad := range map[string]AgentProfile{
		"image and kit":         {Image: "archie-agent:1", Kit: []string{pinned}},
		"unpinned kit":          {Kit: []string{"docker/claude-code-kit:latest"}},
		"unpinned mixin":        {Kit: []string{pinned, "me/setup:1"}},
		"adapter without a kit": {Adapter: "claude-code"},
	} {
		c := ContainerConfig{Profiles: map[string]AgentProfile{"p": bad}}
		if err := c.ValidateProfiles(); err == nil {
			t.Errorf("%s: ValidateProfiles accepted %+v", name, bad)
		}
	}
	if err := c.ValidateProfiles(); err != nil {
		t.Fatalf("a digest-pinned Kit profile was refused: %v", err)
	}
}
