// Package chattask derives the chat task profiles a config allows and adapts
// the State Store's enqueue call to the gateway's task writer.
package chattask

import (
	"context"
	"fmt"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// Writer adapts an enqueue call returning the task to gateway.TaskWriter.
type Writer struct {
	Enqueue func(
		ctx context.Context,
		owner, repo, title, body, workflow, identity, origin string,
		inputs map[string]any,
	) (*workflow.Task, error)
}

func (a Writer) EnqueueChatTask(
	ctx context.Context,
	owner, repo, title, body, workflow, identity, origin string,
	inputs map[string]any,
) (int64, error) {
	task, err := a.Enqueue(ctx, owner, repo, title, body, workflow, identity, origin, inputs)
	if err != nil {
		return 0, err
	}
	if task == nil {
		return 0, fmt.Errorf("enqueue chat task returned no task")
	}
	return task.ID, nil
}

// Profiles returns the task profiles cfg allows and the default identity.
func Profiles(cfg config.Config) ([]gateway.TaskProfile, string) {
	if len(cfg.Identities) > 0 {
		profiles := make([]gateway.TaskProfile, 0, len(cfg.Identities))
		for _, identity := range cfg.Identities {
			if len(identity.Repos) == 0 {
				continue
			}
			profiles = append(profiles, newProfile(identity.Name, identity.Repos))
		}
		if len(profiles) == 0 {
			return nil, ""
		}
		return profiles, profiles[0].Identity
	}
	if len(cfg.Repos) == 0 {
		return nil, ""
	}
	return []gateway.TaskProfile{newProfile("", cfg.Repos)}, ""
}

func newProfile(identity string, repos []config.Repo) gateway.TaskProfile {
	allowed := make([]string, 0, len(repos))
	for _, repo := range repos {
		allowed = append(allowed, repo.Owner+"/"+repo.Name)
	}
	return gateway.TaskProfile{
		Identity:     identity,
		DefaultOwner: repos[0].Owner,
		DefaultRepo:  repos[0].Name,
		Repos:        allowed,
	}
}
