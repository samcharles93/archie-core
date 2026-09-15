package taskactions

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
)

// TestMaxRetriesResolvesFromTheOwningIdentity pins the retry cap's resolution
// order. A multi-identity deployment declares its repositories under
// [[identities.repos]] and has no global [[repos]] at all, so a resolver that
// only walks cfg.Repos never sees the per-repo override and the task is killed
// at the global cap instead of the configured one. The task's stored Identity
// is the only thing that says which identity's repository list owns the row.
func TestMaxRetriesResolvesFromTheOwningIdentity(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		task taskactions.Task
		want int
	}{
		{
			name: "identity repo override wins over the global cap",
			cfg: config.Config{
				MaxRetries: 3,
				Identities: []config.IdentityConfig{{
					Name:  "gh",
					Repos: []config.Repo{{Owner: "acme", Name: "widget", MaxRetries: 7}},
				}},
			},
			task: taskactions.Task{Owner: "acme", Repo: "widget", Identity: "gh"},
			want: 7,
		},
		{
			name: "identity repo with no override uses the global cap",
			cfg: config.Config{
				MaxRetries: 3,
				Identities: []config.IdentityConfig{{
					Name:  "gh",
					Repos: []config.Repo{{Owner: "acme", Name: "widget"}},
				}},
			},
			task: taskactions.Task{Owner: "acme", Repo: "widget", Identity: "gh"},
			want: 3,
		},
		{
			name: "another identity's override does not leak",
			cfg: config.Config{
				MaxRetries: 3,
				Identities: []config.IdentityConfig{
					{Name: "gh", Repos: []config.Repo{{Owner: "acme", Name: "widget", MaxRetries: 7}}},
					{Name: "gitea", Repos: []config.Repo{{Owner: "acme", Name: "widget"}}},
				},
			},
			task: taskactions.Task{Owner: "acme", Repo: "widget", Identity: "gitea"},
			want: 3,
		},
		{
			name: "legacy single-identity repos still resolve",
			cfg: config.Config{
				MaxRetries: 3,
				Repos:      []config.Repo{{Owner: "acme", Name: "widget", MaxRetries: 9}},
			},
			task: taskactions.Task{Owner: "acme", Repo: "widget"},
			want: 9,
		},
		{
			name: "an unknown repository falls back to the global cap",
			cfg:  config.Config{MaxRetries: 5},
			task: taskactions.Task{Owner: "other", Repo: "thing", Identity: "gh"},
			want: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MaxRetries(config.NewHolder(tc.cfg))(&tc.task)
			if got != tc.want {
				t.Fatalf("MaxRetries = %d, want %d (identity %q, repo %s/%s)",
					got, tc.want, tc.task.Identity, tc.task.Owner, tc.task.Repo)
			}
		})
	}
}
