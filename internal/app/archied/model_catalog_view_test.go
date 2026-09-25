package archied

import (
	"reflect"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestCatalogViewListsEachAvailableProviderWithItsModels(t *testing.T) {
	snapshot := modelcatalog.Snapshot{Providers: []modelcatalog.Provider{
		{ID: "openai", Name: "OpenAI", Class: "openai", APIKeyEnv: "OPENAI_API_KEY", Models: []modelcatalog.Model{{ID: "gpt-b"}, {ID: "gpt-a"}}},
		{ID: "anthropic", Name: "Anthropic", Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://api.example", Models: []modelcatalog.Model{{ID: "claude"}}},
	}}
	want := []webui.CatalogProviderView{
		{ID: "anthropic", Name: "Anthropic", Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://api.example", Models: []string{"claude"}},
		{ID: "openai", Name: "OpenAI", Class: "openai", APIKeyEnv: "OPENAI_API_KEY", Models: []string{"gpt-a", "gpt-b"}},
	}
	if got := catalogView(snapshot); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalogView =\n%#v\nwant\n%#v", got, want)
	}
}

func TestCatalogViewOfAnEmptySnapshotIsEmptyNotNil(t *testing.T) {
	got := catalogView(modelcatalog.Snapshot{})
	if got == nil || len(got) != 0 {
		t.Fatalf("catalogView(empty) = %#v, want an empty slice", got)
	}
}
