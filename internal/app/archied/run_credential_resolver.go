package archied

import (
	"context"
	"slices"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/egress"
)

// runCredentialResolver is the egress proxy's Resolver: it resolves a Kit
// credential for the run a run credential names, when the request is made,
// from the live grants and bindings. A revoked credential, a grant removed
// mid-run or a binding in another org stops resolving at once.
//
// A setup session's token names no task, so the resolver consults the session
// grants first: the session is bound for the org's bindings of the Kit's
// declared services, which is the intersection the operator's update-on-secret
// authorisation covers.
type runCredentialResolver struct {
	runs interface {
		TaskForCredential(ctx context.Context, token string) (*workflow.Task, error)
	}
	sessions interface {
		GrantedFor(token string) (org string, services []string, ok bool)
	}
	// models answers a task's org's own providers. Nil grants only the
	// instance providers.
	models  daemon.TaskModels
	config  *config.Holder
	secrets interface {
		Resolve(config.SecretRef) (string, error)
	}
}

func (r runCredentialResolver) Resolve(ctx context.Context, credential, service string) (string, error) {
	if r.sessions != nil {
		if org, services, ok := r.sessions.GrantedFor(credential); ok {
			return r.resolveBinding(org, services, service)
		}
	}
	task, err := r.runs.TaskForCredential(ctx, credential)
	if err != nil {
		return "", egress.ErrUnbound
	}
	cfg := r.config.Get()
	_, granted := cfg.CredentialAccess(task.Identity)
	org := task.OrgOrDefault()
	// A configured model provider's key is granted to every run of the org:
	// any agent stage needs model access.
	if r.isModelProvider(ctx, cfg, task, service) {
		granted = append(slices.Clone(granted), service)
	}
	return r.resolveBinding(org, granted, service)
}

// resolveBinding resolves service from the org's binding when the run or
// session is granted it. An OAuth binding resolves to the empty string: the
// proxy supplies the org's token set itself.
func (r runCredentialResolver) resolveBinding(org string, granted []string, service string) (string, error) {
	cfg := r.config.Get()
	binding, ok := cfg.Containers.BoundCredentials(org, granted, []string{service})[service]
	if !ok {
		return "", egress.ErrUnbound
	}
	if binding.Secret == (config.SecretRef{}) {
		return "", nil
	}
	if r.secrets == nil {
		return "", egress.ErrUnbound
	}
	return r.secrets.Resolve(binding.Secret)
}

// isModelProvider reports whether service is a provider the task's models
// run on: an instance provider, or one of its org's own.
func (r runCredentialResolver) isModelProvider(ctx context.Context, cfg config.Config, task *workflow.Task, service string) bool {
	if _, ok := cfg.Providers[service]; ok {
		return true
	}
	if r.models == nil {
		return false
	}
	models, err := r.models.TaskModels(ctx, task.ID)
	if err != nil {
		return false
	}
	_, ok := models.Providers[service]
	return ok
}
