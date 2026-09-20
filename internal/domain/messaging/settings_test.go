package messaging

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/identity"
)

type settingsClientStub struct {
	descriptors []SettingDescriptor
	resource    SettingResource
	command     SettingCommand
	err         error
	commandErr  error
}

func TestIdentityCommandFailsClosedForSpoofedOrUnknownActor(t *testing.T) {
	trusted, _ := identity.New(identity.StableID("trusted"), identity.KindUser, "trusted")
	target, err := identity.New(identity.StableID("target"), identity.KindBot, "Target")
	if err != nil {
		t.Fatal(err)
	}
	repository := &identityRepositoryStub{values: map[identity.IdentityID]identity.Identity{trusted.ID: trusted, target.ID: target}, aliases: map[string]identity.IdentityID{"trusted": trusted.ID}}
	command := NewSettingsCommand(&settingsClientStub{}).WithIdentities(repository)

	reply := command.Execute(t.Context(), "mallory", "identity suspend "+string(target.ID))
	if !strings.Contains(reply, "did not resolve") {
		t.Fatalf("reply = %q, want fail-closed resolution", reply)
	}
	current, err := repository.Get(t.Context(), target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Lifecycle != identity.LifecycleActive {
		t.Fatalf("spoof changed lifecycle to %q", current.Lifecycle)
	}
	reply = command.Execute(t.Context(), "trusted", "identity suspend "+string(target.ID)+" actor=mallory")
	if !strings.Contains(reply, "Usage:") {
		t.Fatalf("reply = %q, want actor input rejected", reply)
	}
}

type identityRepositoryStub struct {
	values  map[identity.IdentityID]identity.Identity
	aliases map[string]identity.IdentityID
}

func (s *identityRepositoryStub) List(context.Context) ([]identity.Identity, error) {
	out := make([]identity.Identity, 0, len(s.values))
	for _, value := range s.values {
		out = append(out, value)
	}
	return out, nil
}

func (s *identityRepositoryStub) Get(_ context.Context, id identity.IdentityID) (identity.Identity, error) {
	value, ok := s.values[id]
	if !ok {
		return identity.Identity{}, identity.ErrNotFound
	}
	return value, nil
}

func (s *identityRepositoryStub) ResolveLegacyName(ctx context.Context, name string) (identity.Identity, error) {
	return s.Get(ctx, s.aliases[name])
}

func (s *identityRepositoryStub) Create(_ context.Context, value identity.Identity, _ identity.Audit) (identity.Identity, error) {
	s.values[value.ID] = value
	return value, nil
}

func (s *identityRepositoryStub) Apply(ctx context.Context, id identity.IdentityID, version int64, command identity.Command, _ identity.Audit) (identity.Identity, error) {
	current, err := s.Get(ctx, id)
	if err != nil {
		return identity.Identity{}, err
	}
	if current.Version != version {
		return identity.Identity{}, identity.ErrConflict
	}
	updated, _, err := identity.Apply(current, command)
	if err == nil {
		s.values[id] = updated
	}
	return updated, err
}

func (s *settingsClientStub) Catalog(context.Context) ([]SettingDescriptor, error) {
	return s.descriptors, s.err
}

func (s *settingsClientStub) Query(context.Context, string) (SettingResource, error) {
	return s.resource, s.err
}

func (s *settingsClientStub) Command(_ context.Context, command SettingCommand) (SettingResource, error) {
	s.command = command
	return SettingResource{Kind: command.Kind, Version: command.ExpectedVersion + 1, Value: command.Value}, s.commandErr
}

// operatorRepository resolves the one sender the settings tests speak as.
func operatorRepository(name string) (*identityRepositoryStub, identity.Identity) {
	operator, _ := identity.New(identity.StableID(name), identity.KindUser, "operator")
	return &identityRepositoryStub{
		values:  map[identity.IdentityID]identity.Identity{operator.ID: operator},
		aliases: map[string]identity.IdentityID{name: operator.ID},
	}, operator
}

func TestSettingsSetUsesTrustedActorAndCurrentVersion(t *testing.T) {
	client := &settingsClientStub{
		descriptors: []SettingDescriptor{{Kind: "limits", Fields: map[string]SettingField{
			"enabled": {Type: "boolean"}, "retries": {Type: "integer"}, "timeout": {Type: "duration"}, "label": {Type: "string"},
		}}},
		resource: SettingResource{Kind: "limits", Version: 7, Value: map[string]any{"enabled": false, "retries": float64(1), "timeout": "1m", "label": "old"}},
	}
	repository, operator := operatorRepository("archie:operator-7")
	command := NewSettingsCommand(client).WithIdentities(repository)

	reply := command.Execute(t.Context(), "archie:operator-7", `set limits enabled=true retries=3 timeout="2m30s" label="night run"`)

	if !strings.Contains(reply, "version 8") {
		t.Fatalf("reply = %q, want version confirmation", reply)
	}
	if client.command.Actor != string(operator.ID) {
		t.Fatalf("actor = %q, want the resolved identity ID %q", client.command.Actor, operator.ID)
	}
	if client.command.ExpectedVersion != 7 {
		t.Fatalf("expected version = %d, want 7", client.command.ExpectedVersion)
	}
	if _, exists := client.command.Value["actor"]; exists {
		t.Fatal("command text injected an actor field")
	}
	if client.command.RequestID == "" || client.command.Source != "messaging" {
		t.Fatalf("request metadata = (%q, %q), want unique ID and messaging source", client.command.RequestID, client.command.Source)
	}
	previousID := client.command.RequestID
	spoofReply := command.Execute(t.Context(), "archie:operator-7", "set limits actor=mallory")
	if !strings.Contains(spoofReply, "Unknown or non-editable field") || client.command.RequestID != previousID {
		t.Fatalf("actor spoof reply = %q, command unexpectedly dispatched", spoofReply)
	}
	unauthenticatedReply := command.Execute(t.Context(), "", "set limits retries=4")
	if !strings.Contains(unauthenticatedReply, "authenticated Archie identity") || client.command.RequestID != previousID {
		t.Fatalf("unauthenticated reply = %q, command unexpectedly dispatched", unauthenticatedReply)
	}
	unresolvedReply := command.Execute(t.Context(), "mallory", "set limits retries=4")
	if !strings.Contains(unresolvedReply, "did not resolve") || client.command.RequestID != previousID {
		t.Fatalf("unresolved sender reply = %q, command unexpectedly dispatched", unresolvedReply)
	}
}

func TestSettingsSetConflictDoesNotRetryAgainstANewerVersion(t *testing.T) {
	client := &settingsClientStub{
		descriptors: []SettingDescriptor{{Kind: "limits", Fields: map[string]SettingField{"retries": {Type: "integer"}}}},
		resource:    SettingResource{Kind: "limits", Version: 4, Value: map[string]any{"retries": float64(1)}},
		commandErr:  ErrSettingsConflict,
	}
	repository, _ := operatorRepository("archie:operator-7")
	command := NewSettingsCommand(client).WithIdentities(repository)

	reply := command.Execute(t.Context(), "archie:operator-7", "set limits retries=2")

	if !strings.Contains(strings.ToLower(reply), "conflict") {
		t.Fatalf("reply = %q, want conflict guidance", reply)
	}
	if !errors.Is(client.commandErr, ErrSettingsConflict) {
		t.Fatal("stub setup lost conflict")
	}
}

func TestSettingsReplaceAcceptsCollectionJSON(t *testing.T) {
	client := &settingsClientStub{
		descriptors: []SettingDescriptor{{Kind: "schedules"}},
		resource:    SettingResource{Kind: "schedules", Version: 3, Raw: []any{}},
	}
	repository, _ := operatorRepository("archie:operator-7")
	reply := NewSettingsCommand(client).WithIdentities(repository).Execute(t.Context(), "archie:operator-7", `replace schedules [{"id":"daily"}]`)
	if !strings.Contains(reply, "version 4") {
		t.Fatalf("reply = %q", reply)
	}
	values, ok := client.command.RawValue.([]any)
	if !ok || len(values) != 1 || client.command.ExpectedVersion != 3 {
		t.Fatalf("command = %+v", client.command)
	}
}
