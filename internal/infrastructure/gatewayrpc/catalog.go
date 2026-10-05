package gatewayrpc

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/gateway/v1"
)

// SkillEntry is one catalogued skill as the Skills page shows it: what it does
// and where it was discovered.
type SkillEntry struct {
	Name        string
	Description string
	Workflow    string
	Source      string
}

// CuratorState is one registered curator's observable state: identity, health
// and recent activity. HasActivity is false for a curator that has never run.
type CuratorState struct {
	Name           string
	HealthStatus   string
	HealthMessage  string
	HasActivity    bool
	LastRunAt      time.Time
	LastRunActions int
	Recent         []CuratorAction
}

// CuratorAction is one recorded curator action.
type CuratorAction struct {
	At     time.Time
	Type   string
	Detail string
	Reason string
}

// Catalog supplies the process-owned runtime surfaces the dashboard reads over
// ChatService. A zero Catalog answers empty lists and an Unavailable reload,
// which is the honest answer from a process that hosts none of them.
type Catalog struct {
	Skills        func() []SkillEntry
	Curators      func(context.Context) []CuratorState
	ReloadChannel func(context.Context, string) error
}

// CatalogClient is the subset of the Gateway client the dashboard's catalog
// surfaces consume.
type CatalogClient interface {
	ListSkills(ctx context.Context) ([]SkillEntry, error)
	CuratorHealth(ctx context.Context) ([]CuratorState, error)
	ReloadChannel(ctx context.Context, id string) error
}

var _ CatalogClient = (*Client)(nil)

func (s *server) ListSkills(_ context.Context, _ *pb.ListSkillsRequest) (*pb.ListSkillsResponse, error) {
	if s.catalog.Skills == nil {
		return &pb.ListSkillsResponse{}, nil
	}
	entries := s.catalog.Skills()
	out := make([]*pb.Skill, 0, len(entries))
	for _, e := range entries {
		out = append(out, &pb.Skill{
			Name: e.Name, Description: e.Description, Workflow: e.Workflow, Source: e.Source,
		})
	}
	return &pb.ListSkillsResponse{Skills: out}, nil
}

func (s *server) CuratorHealth(ctx context.Context, _ *pb.CuratorHealthRequest) (*pb.CuratorHealthResponse, error) {
	if s.catalog.Curators == nil {
		return &pb.CuratorHealthResponse{}, nil
	}
	states := s.catalog.Curators(ctx)
	out := make([]*pb.Curator, 0, len(states))
	for _, st := range states {
		curator := &pb.Curator{
			Name:           st.Name,
			HealthStatus:   st.HealthStatus,
			HealthMessage:  st.HealthMessage,
			LastRunActions: int64(st.LastRunActions),
		}
		if st.HasActivity {
			curator.LastRunAt = timestamp(st.LastRunAt)
		}
		for _, a := range st.Recent {
			curator.RecentActions = append(curator.RecentActions, &pb.CuratorAction{
				At: timestamp(a.At), Type: a.Type, Detail: a.Detail, Reason: a.Reason,
			})
		}
		out = append(out, curator)
	}
	return &pb.CuratorHealthResponse{Curators: out}, nil
}

func (s *server) ReloadChannel(ctx context.Context, r *pb.ReloadChannelRequest) (*pb.ReloadChannelResponse, error) {
	if s.catalog.ReloadChannel == nil {
		return nil, status.Error(codes.Unavailable, "this gateway hosts no channels")
	}
	if err := s.catalog.ReloadChannel(ctx, r.ChannelId); err != nil {
		return nil, err
	}
	return &pb.ReloadChannelResponse{}, nil
}

func (c *Client) ListSkills(ctx context.Context) ([]SkillEntry, error) {
	v, err := c.client.ListSkills(ctx, &pb.ListSkillsRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]SkillEntry, 0, len(v.Skills))
	for _, s := range v.Skills {
		out = append(out, SkillEntry{Name: s.Name, Description: s.Description, Workflow: s.Workflow, Source: s.Source})
	}
	return out, nil
}

func (c *Client) CuratorHealth(ctx context.Context) ([]CuratorState, error) {
	v, err := c.client.CuratorHealth(ctx, &pb.CuratorHealthRequest{})
	if err != nil {
		return nil, err
	}
	out := make([]CuratorState, 0, len(v.Curators))
	for _, curator := range v.Curators {
		state := CuratorState{
			Name:           curator.Name,
			HealthStatus:   curator.HealthStatus,
			HealthMessage:  curator.HealthMessage,
			LastRunActions: int(curator.LastRunActions),
		}
		if curator.LastRunAt != nil {
			state.HasActivity = true
			state.LastRunAt = curator.LastRunAt.AsTime()
		}
		for _, a := range curator.RecentActions {
			state.Recent = append(state.Recent, CuratorAction{At: timeValue(a.At), Type: a.Type, Detail: a.Detail, Reason: a.Reason})
		}
		out = append(out, state)
	}
	return out, nil
}

func (c *Client) ReloadChannel(ctx context.Context, id string) error {
	_, err := c.client.ReloadChannel(ctx, &pb.ReloadChannelRequest{ChannelId: id})
	return err
}
