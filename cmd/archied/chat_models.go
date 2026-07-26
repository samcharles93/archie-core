package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// chatModelManager owns the process-local model selected for interactive chat.
// The configured role-to-model map supplies the available catalog; switching
// chat models does not rewrite daemon configuration or affect workflow roles.
type chatModelManager struct {
	mu     sync.RWMutex
	models []string
	active string
}

func newChatModelManager(configured map[string]string, catalogs ...[]string) *chatModelManager {
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

	active := strings.TrimSpace(configured["chat"])
	if active == "" {
		active = strings.TrimSpace(configured["builder"])
	}
	return &chatModelManager{models: models, active: active}
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
