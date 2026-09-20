package webui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

const (
	dangerousApprovalLifetime = 10 * time.Minute
	// The "permanent" decision on the wire grants an extended, but still
	// bounded, approval: 24 hours, then requests queue again.
	extendedApprovalLifetime = 24 * time.Hour
)

// DangerousService adapts the daemon-owned sandbox authority to an
// interactive approval workflow. It intentionally has no host process or
// filesystem access of its own.
type DangerousService struct {
	Authority messaging.DangerousCommandAuthority

	mu       sync.Mutex
	actions  map[string]dangerousAction
	extended map[string]time.Time
}

type dangerousAction struct {
	ID          string
	Kind        string
	Spec        string
	Description string
	ExpiresAt   time.Time
}

type DangerousActionView struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func NewDangerousService(authority messaging.DangerousCommandAuthority) *DangerousService {
	return &DangerousService{
		Authority: authority,
		actions:   make(map[string]dangerousAction),
		extended:  make(map[string]time.Time),
	}
}

func (s *DangerousService) enabled() error {
	if s == nil || s.Authority == nil {
		return errors.New("dangerous sandbox actions are not configured")
	}
	return nil
}

func (s *DangerousService) Checkpoints(ctx context.Context) ([]messaging.CheckpointInfo, error) {
	if err := s.enabled(); err != nil {
		return nil, err
	}
	return s.Authority.ListCheckpoints(ctx)
}

func (s *DangerousService) Pending() []DangerousActionView {
	if s == nil {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]DangerousActionView, 0, len(s.actions))
	for id, action := range s.actions {
		if !action.ExpiresAt.After(now) {
			delete(s.actions, id)
			continue
		}
		views = append(views, DangerousActionView{
			ID: action.ID, Kind: action.Kind, Description: action.Description, ExpiresAt: action.ExpiresAt,
		})
	}
	return views
}

// Request creates a pending action unless the local operator has granted a
// still-valid extended (24-hour) approval for this operation family.
func (s *DangerousService) Request(ctx context.Context, kind, spec string) (DangerousActionView, string, bool, error) {
	if err := s.enabled(); err != nil {
		return DangerousActionView{}, "", false, err
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	spec = strings.TrimSpace(spec)
	if kind != "rollback" && kind != "stop" || spec == "" {
		return DangerousActionView{}, "", false, errors.New("invalid dangerous action")
	}
	description := fmt.Sprintf("%s %s", kind, spec)
	s.mu.Lock()
	now := time.Now()
	if expires := s.extended[kind]; expires.After(now) {
		s.mu.Unlock()
		result, err := s.execute(ctx, kind, spec)
		return DangerousActionView{Kind: kind, Description: description, ExpiresAt: expires}, result, true, err
	}
	id := uuid.NewString()
	action := dangerousAction{ID: id, Kind: kind, Spec: spec, Description: description, ExpiresAt: now.Add(dangerousApprovalLifetime)}
	s.actions[id] = action
	s.mu.Unlock()
	return DangerousActionView{ID: id, Kind: kind, Description: description, ExpiresAt: action.ExpiresAt}, "", false, nil
}

func (s *DangerousService) Decide(ctx context.Context, id, decision string) (string, error) {
	if err := s.enabled(); err != nil {
		return "", err
	}
	s.mu.Lock()
	action, ok := s.actions[id]
	if ok {
		delete(s.actions, id)
	}
	s.mu.Unlock()
	if !ok || !action.ExpiresAt.After(time.Now()) {
		return "", errors.New("dangerous action is expired or unknown")
	}
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny":
		return "Command denied.", nil
	case "approve":
		return s.execute(ctx, action.Kind, action.Spec)
	case "permanent":
		// Wire kind "permanent" grants the 24-hour extended approval, not an
		// unlimited one; the literal string is part of the contract with the
		// channels that dispatch decisions (see the telegram gateway).
		s.mu.Lock()
		s.extended[action.Kind] = time.Now().Add(extendedApprovalLifetime)
		s.mu.Unlock()
		return s.execute(ctx, action.Kind, action.Spec)
	default:
		return "", errors.New("decision must be approve, permanent, or deny")
	}
}

func (s *DangerousService) execute(ctx context.Context, kind, spec string) (string, error) {
	if kind == "stop" {
		if err := s.Authority.StopProcess(ctx, spec); err != nil {
			return "", err
		}
		return "Process stopped.", nil
	}
	var checkpoint int
	if _, err := fmt.Sscan(spec, &checkpoint); err != nil || checkpoint <= 0 {
		return "", errors.New("checkpoint must be a positive number")
	}
	return s.Authority.Rollback(ctx, checkpoint)
}
