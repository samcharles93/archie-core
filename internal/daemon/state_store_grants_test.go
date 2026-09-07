package daemon

import (
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
)

// fakeStateStoreGrants issues a fixed, task-scoped token and records revoke
// calls, standing in for staterpc.GrantIssuer in daemon-level tests.
type fakeStateStoreGrants struct {
	token   string
	err     error
	revoked int
}

func (f *fakeStateStoreGrants) Issue(*workflow.Task) (string, func(), error) {
	if f.err != nil {
		return "", nil, f.err
	}
	return f.token, func() { f.revoked++ }, nil
}

func TestContainerEnvNeverExposesStateStoreAdminToken(t *testing.T) {
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), ConnectedStateStore: StateStoreEndpoint{URL: "127.0.0.1:9090", Token: "admin-secret"}}
	for _, entry := range d.containerEnv(&workflow.Task{ID: 42}, "task-scoped-token") {
		if strings.Contains(entry, "admin-secret") {
			t.Fatalf("container receives administrative credential: %s", entry)
		}
	}
}

func TestContainerEnvCarriesTaskScopedStateStoreToken(t *testing.T) {
	d := &Daemon{Cfg: config.NewHolder(config.Config{}), ConnectedStateStore: StateStoreEndpoint{URL: "127.0.0.1:9090", Token: "admin-secret"}}
	got := d.containerEnv(&workflow.Task{ID: 42}, "task-scoped-token")
	if !slices.Contains(got, "STATE_STORE_TOKEN=task-scoped-token") {
		t.Fatalf("containerEnv() = %q, want STATE_STORE_TOKEN=task-scoped-token", got)
	}
}

func TestStateStoreGrantTokenIssuesFromWiredGrants(t *testing.T) {
	grants := &fakeStateStoreGrants{token: "task-scoped-token"}
	d := &Daemon{ConnectedStateStore: StateStoreEndpoint{URL: "127.0.0.1:9090"}, StateStoreGrants: grants}

	token, revoke, err := d.stateStoreGrantToken(&workflow.Task{ID: 42})
	if err != nil {
		t.Fatalf("stateStoreGrantToken: %v", err)
	}
	if token != "task-scoped-token" {
		t.Fatalf("token = %q, want %q", token, "task-scoped-token")
	}
	revoke()
	if grants.revoked != 1 {
		t.Fatalf("revoked = %d, want 1", grants.revoked)
	}
}

func TestStateStoreGrantTokenFailsClosedWithoutIssuer(t *testing.T) {
	d := &Daemon{ConnectedStateStore: StateStoreEndpoint{URL: "127.0.0.1:9090"}}

	if _, _, err := d.stateStoreGrantToken(&workflow.Task{ID: 42}); err == nil {
		t.Fatal("stateStoreGrantToken: want error when State Store is configured but no grant issuer is wired")
	}
}

func TestStateStoreGrantTokenNoOpWhenStateStoreUnconfigured(t *testing.T) {
	d := &Daemon{}

	token, revoke, err := d.stateStoreGrantToken(&workflow.Task{ID: 42})
	if err != nil || token != "" || revoke == nil {
		t.Fatalf("stateStoreGrantToken = (%q, revoke==nil:%v, %v), want (\"\", false, nil)", token, revoke == nil, err)
	}
	revoke() // must not panic
}
