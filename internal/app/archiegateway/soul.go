package archiegateway

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// soulSource resolves the SOUL text a chat turn renders. A stored SOUL owns
// identity once saved; until then the identity's file supplies it, and the
// shipped default is the fallback. It never fails a turn: a read error is
// logged once per failure episode and degrades to the default.
type soulSource struct {
	path string
	log  *slog.Logger

	mu      sync.RWMutex
	owned   string
	failing bool
}

func newSoulSource(configPath string, log *slog.Logger) *soulSource {
	if configPath == "" {
		configPath = configuration.DefaultConfigPath()
	}
	return &soulSource{path: configPath, log: log}
}

// apply stores the text the control plane's soul resource last carried. Empty
// text leaves the SOUL unowned, so the file and default still apply.
func (s *soulSource) apply(owned string) {
	s.mu.Lock()
	s.owned = owned
	s.mu.Unlock()
}

// Soul returns the effective SOUL text for the next turn.
func (s *soulSource) Soul() string {
	s.mu.RLock()
	owned := s.owned
	s.mu.RUnlock()
	if strings.TrimSpace(owned) != "" {
		return owned
	}
	text, err := configuration.ReadSoul(s.path)
	switch {
	case err != nil:
		s.fail("soul file unreadable; using the shipped default", err)
	case strings.TrimSpace(text) == "":
		// No file content: fall through to the shipped default.
	default:
		if err := (agent.Soul{Text: text}).Validate(); err != nil {
			s.fail("soul file invalid; using the shipped default", err)
			break
		}
		s.recover()
		return text
	}
	return agent.ShippedSoul()
}

// fail logs the first failure of an episode; repeats stay quiet until recover.
func (s *soulSource) fail(msg string, err error) {
	s.mu.Lock()
	first := !s.failing
	s.failing = true
	s.mu.Unlock()
	if first && s.log != nil {
		s.log.Warn(msg, "err", err)
	}
}

func (s *soulSource) recover() {
	s.mu.Lock()
	s.failing = false
	s.mu.Unlock()
}
