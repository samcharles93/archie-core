package memory

import (
	"strings"
	"testing"
)

func TestScopeValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scope   Scope
		wantErr string // "" means valid; otherwise a substring of the error
	}{
		{"global", Scope{Kind: ScopeGlobal}, ""},
		{"agent", Scope{Kind: ScopeAgent, Agent: "a1"}, ""},
		{"user", Scope{Kind: ScopeUser, User: "u1"}, ""},
		{"agent-user", Scope{Kind: ScopeAgentUser, Agent: "a1", User: "u1"}, ""},

		{"unknown kind", Scope{Kind: "session"}, "unknown kind"},
		{"empty kind", Scope{}, "unknown kind"},

		{"global rejects agent", Scope{Kind: ScopeGlobal, Agent: "a1"}, "takes no agent or user"},
		{"global rejects user", Scope{Kind: ScopeGlobal, User: "u1"}, "takes no agent or user"},

		{"agent needs an agent", Scope{Kind: ScopeAgent}, "agent must not be empty"},
		{"agent rejects a user", Scope{Kind: ScopeAgent, Agent: "a1", User: "u1"}, "takes no user"},

		{"user needs a user", Scope{Kind: ScopeUser}, "user must not be empty"},
		{"user rejects an agent", Scope{Kind: ScopeUser, Agent: "a1", User: "u1"}, "takes no agent"},

		{"agent-user needs an agent", Scope{Kind: ScopeAgentUser, User: "u1"}, "both required"},
		{"agent-user needs a user", Scope{Kind: ScopeAgentUser, Agent: "a1"}, "both required"},
		{"agent-user needs both", Scope{Kind: ScopeAgentUser}, "both required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.scope.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("Validate() = nil, want an error mentioning %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tt.wantErr)
			}
		})
	}
}

func TestScopeKeyIsCanonical(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scope Scope
		want  string
	}{
		{"global", Scope{Kind: ScopeGlobal}, "global"},
		{"agent", Scope{Kind: ScopeAgent, Agent: "telegram"}, "agent:8:telegram"},
		{"user", Scope{Kind: ScopeUser, User: "u1"}, "user:2:u1"},
		{"agent-user", Scope{Kind: ScopeAgentUser, Agent: "a1", User: "u1"}, "agent-user:2:a1:2:u1"},
		// An invalid scope has no storage key; "" is the signal, never a key
		// a valid scope could produce.
		{"invalid kind", Scope{Kind: "session", Agent: "a1"}, ""},
		{"missing component", Scope{Kind: ScopeAgentUser, Agent: "a1"}, "agent-user:2:a1:0:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.scope.Key(); got != tt.want {
				t.Fatalf("Key() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestScopeKeyCollisionFree is the property Key's length prefix exists for:
// AgentID and IdentityID are channel-native and opaque, so a separator-based
// key would let two different scopes share one storage key. Every scope over
// ids that embed the separator must still key distinctly.
func TestScopeKeyCollisionFree(t *testing.T) {
	t.Parallel()

	ids := []string{"", "a", "a:", ":a", "a:1", "1", "1:a", "agent-user:1:a"}
	distinct := map[Scope]bool{{Kind: ScopeGlobal}: true}
	for _, agent := range ids {
		for _, user := range ids {
			distinct[Scope{Kind: ScopeAgentUser, Agent: AgentID(agent), User: IdentityID(user)}] = true
			distinct[Scope{Kind: ScopeAgent, Agent: AgentID(agent)}] = true
			distinct[Scope{Kind: ScopeUser, User: IdentityID(user)}] = true
		}
	}

	seen := make(map[string]Scope, len(distinct))
	for scope := range distinct {
		if err := scope.Validate(); err != nil {
			continue // an incomplete scope has no key to collide with
		}
		key := scope.Key()
		if key == "" {
			t.Fatalf("valid scope %+v has an empty key", scope)
		}
		if other, dup := seen[key]; dup {
			t.Fatalf("scopes %+v and %+v share key %q", other, scope, key)
		}
		seen[key] = scope
	}
}

func TestSubjectScopesNarrowestFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject Subject
		want    []string
	}{
		{
			name:    "agent and user",
			subject: Subject{AgentID: "a1", UserID: "u1"},
			want:    []string{"agent-user:2:a1:2:u1", "agent:2:a1", "user:2:u1", "global"},
		},
		{
			// A channel with no per-person identity (dashboard, webhook)
			// gets agent and global only -- never a wider scope to stand in
			// for the one it does not know.
			name:    "agent only",
			subject: Subject{AgentID: "a1"},
			want:    []string{"agent:2:a1", "global"},
		},
		{
			name:    "user only",
			subject: Subject{UserID: "u1"},
			want:    []string{"user:2:u1", "global"},
		},
		{
			name:    "neither",
			subject: Subject{},
			want:    []string{"global"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := keys(tt.subject.Scopes())
			if len(got) != len(tt.want) {
				t.Fatalf("Scopes() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("Scopes() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestSubjectWritableScopesExcludeGlobal(t *testing.T) {
	t.Parallel()

	subject := Subject{AgentID: "a1", UserID: "u1"}
	writable := keys(subject.WritableScopes())
	if len(writable) != 3 {
		t.Fatalf("WritableScopes() = %v, want three scopes", writable)
	}
	for _, key := range writable {
		if key == "global" {
			t.Fatal("WritableScopes() includes global, which is never model-writable")
		}
	}
	// Every writable scope is readable: the read set is a superset.
	readable := map[string]bool{}
	for _, key := range keys(subject.Scopes()) {
		readable[key] = true
	}
	for _, key := range writable {
		if !readable[key] {
			t.Fatalf("writable scope %q is not in the read set", key)
		}
	}
}

func keys(scopes []Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		out = append(out, scope.Key())
	}
	return out
}

func TestNewRecordValidate(t *testing.T) {
	t.Parallel()

	valid := Scope{Kind: ScopeAgent, Agent: "a1"}
	tests := []struct {
		name    string
		in      NewRecord
		wantErr string
	}{
		{"valid", NewRecord{Scope: valid, Content: "x"}, ""},
		{"valid without kind", NewRecord{Scope: valid, Content: "x"}, ""},
		{"empty content", NewRecord{Scope: valid}, "content must not be empty"},
		{"invalid scope", NewRecord{Scope: Scope{Kind: ScopeAgent}, Content: "x"}, "agent must not be empty"},
		{"no scope", NewRecord{Content: "x"}, "unknown kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertValidation(t, tt.in.Validate(), tt.wantErr)
		})
	}
}

func TestRecordUpdateValidate(t *testing.T) {
	t.Parallel()

	valid := Scope{Kind: ScopeAgent, Agent: "a1"}
	tests := []struct {
		name    string
		in      RecordUpdate
		wantErr string
	}{
		{"valid", RecordUpdate{Scope: valid, ID: "r1", Content: "x"}, ""},
		{"valid with expected", RecordUpdate{Scope: valid, ID: "r1", Content: "x", Expected: 2}, ""},
		{"empty id", RecordUpdate{Scope: valid, Content: "x"}, "id must not be empty"},
		{"empty content", RecordUpdate{Scope: valid, ID: "r1"}, "content must not be empty"},
		{"invalid scope", RecordUpdate{Scope: Scope{Kind: ScopeGlobal, Agent: "a1"}, ID: "r1", Content: "x"}, "takes no agent or user"},
		{"negative expected", RecordUpdate{Scope: valid, ID: "r1", Content: "x", Expected: -1}, "must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertValidation(t, tt.in.Validate(), tt.wantErr)
		})
	}
}

func TestQueryValidate(t *testing.T) {
	t.Parallel()

	agent := Scope{Kind: ScopeAgent, Agent: "a1"}
	tests := []struct {
		name    string
		q       Query
		wantErr string
	}{
		{"one scope", Query{Scopes: []Scope{agent}}, ""},
		{"two scopes", Query{Scopes: []Scope{agent, {Kind: ScopeGlobal}}}, ""},
		{"scope only, no text", Query{Scopes: []Scope{agent}}, ""},
		{"with limit", Query{Scopes: []Scope{agent}, Limit: 5}, ""},
		{"no scopes", Query{}, "at least one scope"},
		{"invalid scope", Query{Scopes: []Scope{{Kind: ScopeAgentUser, Agent: "a1"}}}, "both required"},
		{"negative limit", Query{Scopes: []Scope{agent}, Limit: -1}, "must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertValidation(t, tt.q.Validate(), tt.wantErr)
		})
	}
}

func TestManifestValidateAcceptsZeroValue(t *testing.T) {
	t.Parallel()

	if err := (Manifest{}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil: no field is required yet", err)
	}
}

func assertValidation(t *testing.T, err error, wantErr string) {
	t.Helper()
	switch {
	case wantErr == "" && err != nil:
		t.Fatalf("Validate() = %v, want nil", err)
	case wantErr != "" && err == nil:
		t.Fatalf("Validate() = nil, want an error mentioning %q", wantErr)
	case wantErr != "" && !strings.Contains(err.Error(), wantErr):
		t.Fatalf("Validate() = %v, want an error mentioning %q", err, wantErr)
	}
}
