package agentexec

import (
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestProvidersFromConfig(t *testing.T) {
	in := map[string]config.Provider{
		"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://api.anthropic.com"},
	}
	want := map[string]Provider{
		"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://api.anthropic.com"},
	}
	if got := ProvidersFromConfig(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("ProvidersFromConfig() = %#v, want %#v", got, want)
	}
}

func TestProvidersFromConfigEmpty(t *testing.T) {
	if got := ProvidersFromConfig(nil); len(got) != 0 {
		t.Fatalf("ProvidersFromConfig(nil) = %#v, want empty", got)
	}
}
