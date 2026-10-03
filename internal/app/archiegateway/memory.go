package archiegateway

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/gateway"
	infraMemory "github.com/samcharles93/archie-core/internal/infrastructure/memory"
)

func (b *server) setupMemoryEngine() error {
	cfg, log := b.cfg, b.log
	registry := domainmemory.NewRegistry(domainmemory.Registrar{Log: log})

	switch cfg.Memory.Engine {
	case infraMemory.EngineName, "":
		root := filepath.Join(cfg.WorkDir, "memory-engine")
		if err := registry.Register(infraMemory.NewBuiltinEngine(root, 0)); err != nil {
			return fmt.Errorf("register memory engine %q: %w", infraMemory.EngineName, err)
		}
	default:
		return fmt.Errorf("memory.engine %q has no registered implementation", cfg.Memory.Engine)
	}

	if err := registry.Start(context.Background()); err != nil {
		return fmt.Errorf("start memory engine registry: %w", err)
	}
	b.memEngines = registry
	b.addCleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := registry.Stop(shutdownCtx); err != nil {
			log.Error("memory engine registry shutdown", "err", err)
		}
	})
	log.Info("memory engine started", "engine", cfg.Memory.Engine)
	return nil
}

func (b *server) activeMemoryEngine() (domainmemory.MemoryEngine, bool) {
	if b.memEngines == nil {
		return nil, false
	}
	name := b.cfg.Memory.Engine
	if name == "" {
		name = infraMemory.EngineName
	}
	return b.memEngines.Get(name)
}

func (b *server) memoryStore() gateway.MemoryStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}

func (b *server) memoryWriter() gateway.MemoryWriteStore {
	engine, ok := b.activeMemoryEngine()
	if !ok {
		return nil
	}
	return engine
}
