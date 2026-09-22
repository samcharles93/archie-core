package edastore

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/binding"
)

// bindingInput is the small shape the cipher tests need, kept separate from
// binding.Binding so a domain field addition does not churn every test.
type bindingInput struct {
	Name     string
	Source   string
	Workflow string
	Secret   string
}

func insertBinding(t *testing.T, s *Store, in bindingInput) string {
	t.Helper()
	id, err := s.InsertBinding(t.Context(), binding.Binding{
		Name:     in.Name,
		Matcher:  binding.Matcher{Source: in.Source},
		Workflow: in.Workflow,
		Secret:   in.Secret,
	})
	if err != nil {
		t.Fatalf("InsertBinding() error = %v", err)
	}
	return id
}
