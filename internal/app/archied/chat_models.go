package archied

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
)

// chatModelManager owns the process-local model selected for interactive chat.
// The configured role-to-model map supplies the available catalog; switching
// chat models does not rewrite daemon configuration or affect workflow roles.
type chatModelManager struct {
	mu     sync.RWMutex
	models []string
	active string
	// catalogs are the non-role sources the model set is derived from, kept
	// so SetConfigured can rebuild it after a live settings update without a
	// caller re-passing them.
	catalogs      [][]string
	details       map[string]gateway.ModelDetails
	providerNames map[string]string
}

// pickChatDefault is the model the manager starts on: the chat role's
// assignment, falling back to the builder role's.
func pickChatDefault(configured map[string]string) string {
	if active := strings.TrimSpace(configured["chat"]); active != "" {
		return active
	}
	return strings.TrimSpace(configured["builder"])
}

// mergeModelRefs is the model set the manager offers: the configured role
// assignments plus every catalog, unique and sorted.
func mergeModelRefs(configured map[string]string, catalogs ...[]string) []string {
	unique := make(map[string]struct{}, len(configured))
	for _, ref := range configured {
		ref = strings.TrimSpace(ref)
		if ref != "" {
			unique[ref] = struct{}{}
		}
	}
	for _, catalog := range catalogs {
		for _, ref := range catalog {
			ref = strings.TrimSpace(ref)
			if ref != "" {
				unique[ref] = struct{}{}
			}
		}
	}

	models := make([]string, 0, len(unique))
	for ref := range unique {
		models = append(models, ref)
	}
	slices.Sort(models)
	return models
}

func newChatModelManager(configured map[string]string, catalogs ...[]string) *chatModelManager {
	return &chatModelManager{
		models:        mergeModelRefs(configured, catalogs...),
		active:        pickChatDefault(configured),
		catalogs:      append([][]string(nil), catalogs...),
		details:       make(map[string]gateway.ModelDetails),
		providerNames: make(map[string]string),
	}
}

// SetConfigured re-derives the model set the manager offers after a live
// change to model-role-assignments or provider-settings: the list is built
// from the configured roles at construction, so a stored change would leave
// the chat surfaces offering refs the runtime can no longer resolve. The
// operator's active selection survives when the new roles still carry it; a
// removed one falls back the way construction does.
func (m *chatModelManager) SetConfigured(configured map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	models := mergeModelRefs(configured, m.catalogs...)
	if !slices.Contains(models, m.active) {
		m.active = pickChatDefault(configured)
	}
	m.models = models
}

func (m *chatModelManager) ApplyModelCatalog(snapshot modelcatalog.Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, provider := range snapshot.Providers {
		m.providerNames[provider.ID] = provider.Name
		for _, model := range provider.Models {
			ref := provider.ID + "/" + model.ID
			m.details[ref] = gateway.ModelDetails{
				Ref: ref, Name: model.Name,
				ContextWindow: model.ContextWindow, MaxOutputTokens: model.MaxOutputTokens,
				Reasoning: model.Reasoning, Tools: true,
				Attachment: model.Attachment, Structured: model.Structured,
				InputModalities: slices.Clone(model.InputModalities),
			}
		}
	}
}

func (m *chatModelManager) ProviderDisplayName(provider string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providerNames[provider]
}

func (m *chatModelManager) ModelDetails(ref string) (gateway.ModelDetails, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	details, ok := m.details[ref]
	return details, ok
}

func (m *chatModelManager) Models() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.models)
}

func (m *chatModelManager) ActiveModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

func (m *chatModelManager) SetActiveModel(ctx context.Context, ref string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.Contains(m.models, ref) {
		return fmt.Errorf("model %q is not configured", ref)
	}
	m.active = ref
	return nil
}

func (m *chatModelManager) Providers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := make(map[string]bool)
	var providers []string
	for _, model := range m.models {
		provider, _, ok := strings.Cut(model, "/")
		if ok && provider != "" && !seen[provider] {
			seen[provider] = true
			providers = append(providers, provider)
		}
	}
	slices.Sort(providers)
	return providers
}

func (m *chatModelManager) ActiveProvider() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	provider, _, _ := strings.Cut(m.active, "/")
	return provider
}

func (m *chatModelManager) ModelsForProvider(provider string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix := provider + "/"
	var models []string
	for _, model := range m.models {
		if strings.HasPrefix(model, prefix) {
			models = append(models, model)
		}
	}
	return models
}

func (m *chatModelManager) SetActiveProvider(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := provider + "/"
	for _, model := range m.models {
		if strings.HasPrefix(model, prefix) {
			m.active = model
			return nil
		}
	}
	return fmt.Errorf("provider %q is not configured", provider)
}
