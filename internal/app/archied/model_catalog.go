package archied

import (
	"slices"
	"strings"

	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/webui"
)

// catalogView renders the usable providers for the published config view,
// sorted by id with each provider's model ids sorted, so the document is
// stable across publishes.
func catalogView(snapshot modelcatalog.Snapshot) []webui.CatalogProviderView {
	view := make([]webui.CatalogProviderView, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			models = append(models, model.ID)
		}
		slices.Sort(models)
		view = append(view, webui.CatalogProviderView{
			ID: provider.ID, Name: provider.Name, Class: provider.Class,
			APIKeyEnv: provider.APIKeyEnv, BaseURL: provider.BaseURL, Models: models,
		})
	}
	slices.SortFunc(view, func(a, b webui.CatalogProviderView) int { return strings.Compare(a.ID, b.ID) })
	return view
}
