package archied

import (
	"context"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// runCredentialResolver is the egress proxy's Resolver: it resolves a Kit
// credential for the run a run credential names, when the request is made,
// from the live grants and bindings. A revoked credential, a grant removed
// mid-run or a binding in another org stops resolving at once.
type runCredentialResolver struct {
	runs interface {
		TaskForCredential(ctx context.Context, token string) (*workflow.Task, error)
	}
	config  *config.Holder
	secrets interface {
		Resolve(config.SecretRef) (string, error)
	}
}

func (r runCredentialResolver) Resolve(ctx context.Context, credential, service string) (string, error) {
	task, err := r.runs.TaskForCredential(ctx, credential)
	if err != nil {
		return "", egress.ErrUnbound
	}
	cfg := r.config.Get()
	org, granted := cfg.CredentialAccess(task.Identity)
	binding, ok := cfg.Containers.BoundCredentials(org, granted, []string{service})[service]
	if !ok {
		return "", egress.ErrUnbound
	}
	if binding.Secret == (config.SecretRef{}) {
		// An OAuth binding: the proxy supplies the org's token set itself.
		return "", nil
	}
	return r.secrets.Resolve(binding.Secret)
}
