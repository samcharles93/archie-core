package identity

import (
	"errors"
	"testing"
)

func TestSystemIdentityCannotBeMutated(t *testing.T) {
	t.Parallel()
	system := System()
	for name, command := range map[string]Command{
		"rename":     {Type: CommandRename, DisplayName: "Other"},
		"suspend":    {Type: CommandSuspend},
		"reactivate": {Type: CommandReactivate},
		"retire":     {Type: CommandRetire},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Apply(system, command); !errors.Is(err, ErrSystemImmutable) {
				t.Fatalf("Apply() error = %v, want ErrSystemImmutable", err)
			}
		})
	}
}

func TestLifecycleTransitions(t *testing.T) {
	t.Parallel()
	id, err := New(StableID("reviewer"), KindBot, "Reviewer")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{{Type: CommandSuspend}, {Type: CommandReactivate}, {Type: CommandRetire}} {
		var event Event
		id, event, err = Apply(id, command)
		if err != nil {
			t.Fatalf("Apply(%s): %v", command.Type, err)
		}
		if event.IdentityID != id.ID {
			t.Fatalf("event identity = %q, want %q", event.IdentityID, id.ID)
		}
	}
	if id.Lifecycle != LifecycleRetired {
		t.Fatalf("lifecycle = %q, want retired", id.Lifecycle)
	}
	if _, _, err = Apply(id, Command{Type: CommandReactivate}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("reactivate retired error = %v, want ErrIllegalTransition", err)
	}
}

func TestStableIDIsNameStableAndNotDisplayName(t *testing.T) {
	t.Parallel()
	first := StableID(" ReviewER ")
	if first != StableID("reviewer") {
		t.Fatalf("stable IDs differ: %q", first)
	}
	if first == IdentityID("reviewer") {
		t.Fatal("stable ID aliases the legacy display name")
	}
}
