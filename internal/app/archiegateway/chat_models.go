package archiegateway

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
)

// chatModelManager owns the process-local model alias selected for
// interactive chat. Switching it does not rewrite configuration or affect
// workflow steps.
type chatModelManager struct {
	mu            sync.RWMutex
	aliases       map[string]string
	active        string
	details       map[string]gateway.ModelDetails
	providerNames map[string]string
}

func newChatModelManager(aliases map[string]string) *chatModelManager {
	m := &chatModelManager{details: make(map[string]gateway.ModelDetails), providerNames: make(map[string]string)}
	m.SetConfigured(aliases)
	return m
}

// SetConfigured replaces the alias table after a live change to model-aliases.
// The active alias survives when it still exists; otherwise chat returns to
// the default alias.
func (m *chatModelManager) SetConfigured(aliases map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.aliases = aliases
	if !slices.Contains(config.ChatAliases(aliases), m.active) {
		m.active = config.PurposeChat.DefaultAlias()
	}
}

// SetModelCatalog replaces the provider display names and per-model details
// the catalog publishes.
func (m *chatModelManager) SetModelCatalog(snapshot modelcatalog.Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.details = make(map[string]gateway.ModelDetails)
	m.providerNames = make(map[string]string)
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

// Models returns the aliases chat may switch between.
func (m *chatModelManager) Models() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return config.ChatAliases(m.aliases)
}

func (m *chatModelManager) ActiveAlias() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// ActiveModel returns the active alias's model ref, or "" when it is unset.
func (m *chatModelManager) ActiveModel() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ref, _ := config.ResolveModel(m.aliases, m.active, config.PurposeChat)
	return ref
}

func (m *chatModelManager) SetActiveModel(ctx context.Context, alias string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.Contains(config.ChatAliases(m.aliases), alias) {
		return fmt.Errorf("model alias %q is not configured", alias)
	}
	m.active = alias
	return nil
}
