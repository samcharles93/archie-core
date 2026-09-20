// Package identity owns persistent actor identity and lifecycle semantics.
package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

type (
	IdentityID  string
	Kind        string
	Lifecycle   string
	CommandType string
)

const (
	SystemID           IdentityID  = "00000000-0000-5000-8000-000000000001"
	KindSystem         Kind        = "system"
	KindBot            Kind        = "bot"
	KindServiceAccount Kind        = "service_account"
	KindUser           Kind        = "user"
	LifecycleActive    Lifecycle   = "active"
	LifecycleSuspended Lifecycle   = "suspended"
	LifecycleRetired   Lifecycle   = "retired"
	CommandRename      CommandType = "rename"
	CommandSuspend     CommandType = "suspend"
	CommandReactivate  CommandType = "reactivate"
	CommandRetire      CommandType = "retire"
)

var (
	ErrInvalid           = errors.New("invalid identity")
	ErrNotFound          = errors.New("identity not found")
	ErrConflict          = errors.New("identity version conflict")
	ErrSystemImmutable   = errors.New("system identity is immutable")
	ErrIllegalTransition = errors.New("illegal identity lifecycle transition")
)

type Identity struct {
	ID          IdentityID `json:"id"`
	Kind        Kind       `json:"kind"`
	DisplayName string     `json:"display_name"`
	Lifecycle   Lifecycle  `json:"lifecycle"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type (
	Command struct {
		Type        CommandType
		DisplayName string
	}
	Event struct {
		IdentityID  IdentityID
		Type        CommandType
		From        Lifecycle
		To          Lifecycle
		DisplayName string
		At          time.Time
	}
	Audit struct {
		ActorID           IdentityID
		Source, RequestID string
		At                time.Time
	}
)

type Repository interface {
	List(context.Context) ([]Identity, error)
	Get(context.Context, IdentityID) (Identity, error)
	ResolveLegacyName(context.Context, string) (Identity, error)
	Create(context.Context, Identity, Audit) (Identity, error)
	Apply(context.Context, IdentityID, int64, Command, Audit) (Identity, error)
}

func System() Identity {
	return Identity{ID: SystemID, Kind: KindSystem, DisplayName: "System", Lifecycle: LifecycleActive, Version: 1}
}

func StableID(legacyName string) IdentityID {
	sum := sha256.Sum256([]byte("archie.identity.v1\x00" + strings.ToLower(strings.TrimSpace(legacyName))))
	return IdentityID(fmt.Sprintf("%08x-%04x-5%03x-%04x-%012x", sum[:4], sum[4:6], sum[6:8], uint16(sum[8])<<8|uint16(sum[9])&0x3fff|0x8000, sum[10:16]))
}

func New(id IdentityID, kind Kind, displayName string) (Identity, error) {
	value := Identity{ID: id, Kind: kind, DisplayName: strings.TrimSpace(displayName), Lifecycle: LifecycleActive, Version: 1}
	if err := value.Validate(); err != nil {
		return Identity{}, err
	}
	return value, nil
}

func (i Identity) Validate() error {
	if i.ID == "" || strings.TrimSpace(i.DisplayName) == "" {
		return fmt.Errorf("%w: ID and display name are required", ErrInvalid)
	}
	switch i.Kind {
	case KindSystem, KindBot, KindServiceAccount, KindUser:
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalid, i.Kind)
	}
	switch i.Lifecycle {
	case LifecycleActive, LifecycleSuspended, LifecycleRetired:
	default:
		return fmt.Errorf("%w: unknown lifecycle %q", ErrInvalid, i.Lifecycle)
	}
	if i.Kind == KindSystem && (i.ID != SystemID || i.Lifecycle != LifecycleActive) {
		return ErrSystemImmutable
	}
	if i.ID == SystemID && i.Kind != KindSystem {
		return ErrSystemImmutable
	}
	return nil
}

func Apply(i Identity, command Command) (Identity, Event, error) {
	if i.ID == SystemID || i.Kind == KindSystem {
		return Identity{}, Event{}, ErrSystemImmutable
	}
	event := Event{IdentityID: i.ID, Type: command.Type, From: i.Lifecycle, To: i.Lifecycle, At: time.Now().UTC()}
	switch command.Type {
	case CommandRename:
		name := strings.TrimSpace(command.DisplayName)
		if name == "" {
			return Identity{}, Event{}, fmt.Errorf("%w: display name is required", ErrInvalid)
		}
		i.DisplayName, event.DisplayName = name, name
	case CommandSuspend:
		if i.Lifecycle != LifecycleActive {
			return Identity{}, Event{}, ErrIllegalTransition
		}
		i.Lifecycle, event.To = LifecycleSuspended, LifecycleSuspended
	case CommandReactivate:
		if i.Lifecycle != LifecycleSuspended {
			return Identity{}, Event{}, ErrIllegalTransition
		}
		i.Lifecycle, event.To = LifecycleActive, LifecycleActive
	case CommandRetire:
		if i.Lifecycle != LifecycleActive && i.Lifecycle != LifecycleSuspended {
			return Identity{}, Event{}, ErrIllegalTransition
		}
		i.Lifecycle, event.To = LifecycleRetired, LifecycleRetired
	default:
		return Identity{}, Event{}, fmt.Errorf("%w: unknown command %q", ErrInvalid, command.Type)
	}
	i.Version++
	return i, event, nil
}
