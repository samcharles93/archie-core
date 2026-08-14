package taskrun

import (
	"encoding/json"
	"testing"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestRequestJSONRoundTrip(t *testing.T) {
	req := Request{
		Task: &store.Task{
			ID:          1,
			Owner:       "acme",
			Repo:        "widget",
			IssueNumber: 42,
			Title:       "feat: thing",
			Status:      store.StatusRunning,
		},
		Repo: config.Repo{Owner: "acme", Name: "widget", Base: "main"},
		Cfg:  config.Config{DiffCapLines: 500}.ForTask(),
		Providers: map[string]agentexec.Provider{
			"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got Request
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Task == nil || got.Task.ID != 1 || got.Task.Owner != "acme" || got.Task.IssueNumber != 42 {
		t.Fatalf("Task did not round-trip: %+v", got.Task)
	}
	if got.Repo.FullName() != "acme/widget" || got.Repo.BaseBranch() != "main" {
		t.Fatalf("Repo did not round-trip: %+v", got.Repo)
	}
	if got.Cfg.DiffCapLines != 500 {
		t.Fatalf("Cfg did not round-trip: %+v", got.Cfg)
	}
	if got.Providers["anthropic"].APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("Providers did not round-trip: %+v", got.Providers)
	}
}

func TestRequestValidateRequiresPositiveTaskID(t *testing.T) {
	for _, test := range []struct {
		name    string
		request Request
		wantErr bool
	}{
		{name: "missing task", wantErr: true},
		{name: "zero ID", request: Request{Task: &store.Task{}}, wantErr: true},
		{name: "negative ID", request: Request{Task: &store.Task{ID: -1}}, wantErr: true},
		{name: "positive ID", request: Request{Task: &store.Task{ID: 1}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.request.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestResponseJSONRoundTrip(t *testing.T) {
	resp := Response{
		Task:   &store.Task{ID: 1, Status: store.StatusPROpen},
		Status: "passed",
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Task == nil || got.Task.Status != store.StatusPROpen || got.Status != "passed" {
		t.Fatalf("Response did not round-trip: %+v", got)
	}
}
