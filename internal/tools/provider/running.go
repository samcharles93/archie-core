package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// ErrRegistryNotRunning is returned by the running-state operations when the
// family has been stopped or failed and can no longer change shape.
var ErrRegistryNotRunning = errors.New("tool provider registry is not running")

// Running reports whether the family has started and its providers are live.
func (r *Registry) Running() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state == stateRunning
}

// Has reports whether a provider with id is registered, running or not.
func (r *Registry) Has(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.providers[id]
	return ok
}

// RegisteredIDs returns the manifest IDs of every registered provider, sorted.
func (r *Registry) RegisteredIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// RunningIDs returns the manifest IDs of the running providers, in start
// order. An unchanged provider's presence here is what a live reconciliation
// reads to leave it untouched.
func (r *Registry) RunningIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.running))
	for _, rp := range r.running {
		ids = append(ids, rp.registration.manifest.ID)
	}
	return ids
}

// Add starts a provider while the registry runs. A failed start removes it.
// Before Start, it only registers.
func (r *Registry) Add(ctx context.Context, engine Engine) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	reg, err := prepareRegistration(engine, true)
	if err != nil {
		return err
	}
	id := reg.manifest.ID

	r.mu.Lock()
	if _, exists := r.providers[id]; exists {
		r.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrDuplicateProvider, id)
	}
	switch r.state {
	case stateIdle:
		r.providers[id] = reg
		r.mu.Unlock()
		return nil
	case stateRunning:
	default:
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("%w: cannot add from state %d", ErrRegistryNotRunning, state)
	}
	if missing, blocked := r.missingDependencyLocked(reg); blocked {
		r.mu.Unlock()
		return fmt.Errorf("tool provider %q depends on %q, which is not running", id, missing)
	}
	r.providers[id] = reg
	r.mu.Unlock()

	rp, err := r.startProvider(ctx, reg, nil)
	if err != nil {
		r.mu.Lock()
		delete(r.providers, id)
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.running = append(r.running, rp)
	r.mu.Unlock()
	return nil
}

// Remove stops one running provider and unregisters it, removing the tools it
// indexed. An MCP server is a child process with a Stop, so a removal really
// unloads. On a registry that has not started,
// Remove only unregisters.
func (r *Registry) Remove(ctx context.Context, id string) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	r.mu.Lock()
	if _, exists := r.providers[id]; !exists {
		r.mu.Unlock()
		return fmt.Errorf("tool provider %q is not registered", id)
	}
	switch r.state {
	case stateIdle, stateRunning:
	default:
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("%w: cannot remove from state %d", ErrRegistryNotRunning, state)
	}
	index := -1
	for i, rp := range r.running {
		if rp.registration.manifest.ID == id {
			index = i
			break
		}
	}
	if index >= 0 {
		if dependent, ok := r.runningDependentLocked(id); ok {
			r.mu.Unlock()
			return fmt.Errorf("tool provider %q is required by running provider %q and cannot be removed", id, dependent)
		}
	}
	var rp runningProvider
	if index >= 0 {
		rp = r.running[index]
		r.running = slices.Delete(r.running, index, index+1)
	}
	delete(r.providers, id)
	r.mu.Unlock()

	if index < 0 {
		return nil
	}
	r.index.Unregister(rp.toolNames...)
	if err := safeStop(ctx, rp.registration.engine); err != nil {
		return fmt.Errorf("stop tool provider %q: %w", id, err)
	}
	return nil
}

// Replace swaps the running provider with the same id for engine, restoring
// the old one if the new one fails. Before Start, it only swaps the
// registration.
func (r *Registry) Replace(ctx context.Context, engine Engine) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()

	reg, err := prepareRegistration(engine, true)
	if err != nil {
		return err
	}
	id := reg.manifest.ID

	r.mu.Lock()
	if _, exists := r.providers[id]; !exists {
		r.mu.Unlock()
		return fmt.Errorf("tool provider %q is not registered", id)
	}
	switch r.state {
	case stateIdle:
		r.providers[id] = reg
		r.mu.Unlock()
		return nil
	case stateRunning:
	default:
		state := r.state
		r.mu.Unlock()
		return fmt.Errorf("%w: cannot replace from state %d", ErrRegistryNotRunning, state)
	}
	index := -1
	for i, rp := range r.running {
		if rp.registration.manifest.ID == id {
			index = i
			break
		}
	}
	if missing, blocked := r.missingDependencyLocked(reg); blocked {
		r.mu.Unlock()
		return fmt.Errorf("tool provider %q depends on %q, which is not running", id, missing)
	}
	if index < 0 {
		// Registered but not running -- a refused earlier start. Register the
		// replacement and start it alone; nothing is running to stop first.
		r.providers[id] = reg
		r.mu.Unlock()
		return r.startRegistered(ctx, reg)
	}
	if dependent, ok := r.runningDependentLocked(id); ok {
		r.mu.Unlock()
		return fmt.Errorf("tool provider %q is required by running provider %q and cannot be replaced", id, dependent)
	}
	previous := r.running[index]
	r.mu.Unlock()

	// The old provider's tools leave the index before the new provider
	// discovers, or a replacement advertising the same tool names would
	// collide with its own predecessor.
	r.index.Unregister(previous.toolNames...)
	if err := safeStop(ctx, previous.registration.engine); err != nil {
		r.dropProvider(id)
		return fmt.Errorf("stop tool provider %q: %w", id, err)
	}
	rp, err := r.startProvider(ctx, reg, nil)
	if err != nil {
		restartErr := r.restartProvider(ctx, previous)
		return errors.Join(fmt.Errorf("replace tool provider %q: %w", id, err), restartErr)
	}
	r.mu.Lock()
	r.providers[id] = reg
	r.running[index] = rp
	r.mu.Unlock()
	return nil
}

// startRegistered starts one already-registered provider alone and moves it
// into the running set, rolling the registration back when it cannot run.
func (r *Registry) startRegistered(ctx context.Context, reg registration) error {
	id := reg.manifest.ID
	rp, err := r.startProvider(ctx, reg, nil)
	if err != nil {
		r.mu.Lock()
		delete(r.providers, id)
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.running = append(r.running, rp)
	r.mu.Unlock()
	return nil
}

// restartProvider puts a stopped provider back the way it was, the rollback of
// a refused replacement. It restarts the engine and re-indexes the tools it
// advertised; when even that fails, the provider is dropped rather than left
// half-registered, because nothing can put it back.
func (r *Registry) restartProvider(ctx context.Context, previous runningProvider) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	id := previous.registration.manifest.ID
	if err := safeStart(cleanupCtx, previous.registration.engine); err != nil {
		r.dropProvider(id)
		return fmt.Errorf("restart tool provider %q: %w", id, err)
	}
	entries, err := safeDiscover(cleanupCtx, previous.registration.engine)
	if err == nil {
		err = r.index.RegisterBatch(entries)
	}
	if err != nil {
		r.dropProvider(id)
		return fmt.Errorf("restore tool provider %q: %w", id, err)
	}
	return nil
}

// dropProvider removes a provider from both the registration and the running
// set, the disposition of a provider that could not be started or restored.
func (r *Registry) dropProvider(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, id)
	r.running = slices.DeleteFunc(r.running, func(rp runningProvider) bool {
		return rp.registration.manifest.ID == id
	})
}

// missingDependencyLocked reports the first dependency of reg that is not in
// the running set. The caller holds r.mu.
func (r *Registry) missingDependencyLocked(reg registration) (string, bool) {
	for _, dependency := range reg.manifest.Dependencies {
		found := false
		for _, rp := range r.running {
			if rp.registration.manifest.ID == dependency {
				found = true
				break
			}
		}
		if !found {
			return dependency, true
		}
	}
	return "", false
}

// runningDependentLocked returns a running provider other than id that depends
// on id, which is what makes replacing or removing id unsafe. The caller holds
// r.mu.
func (r *Registry) runningDependentLocked(id string) (string, bool) {
	for _, rp := range r.running {
		if rp.registration.manifest.ID == id {
			continue
		}
		if slices.Contains(rp.registration.manifest.Dependencies, id) {
			return rp.registration.manifest.ID, true
		}
	}
	return "", false
}
