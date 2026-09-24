package staterpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/eventtype"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

var (
	_ storecontract.EventTypeStore = (*Client)(nil)

	errEventTypeUnavailable = status.Error(codes.Unavailable, "event type store unavailable")
)

func eventTypeProto(t eventtype.EventType) *pb.EventType {
	out := &pb.EventType{
		Id: t.ID, Source: t.Source, Name: t.Name,
		CreatedAt: timestamp(t.CreatedAt), UpdatedAt: timestamp(t.UpdatedAt),
	}
	for _, h := range t.Rule.Headers {
		out.HeaderConditions = append(out.HeaderConditions, &pb.EventTypeHeaderCondition{Name: h.Name, Value: h.Value})
	}
	for _, p := range t.Rule.Payload {
		out.PayloadConditions = append(out.PayloadConditions, &pb.EventTypePayloadCondition{Path: p.Path, Op: string(p.Op), Value: p.Value})
	}
	if len(t.Schema) > 0 {
		out.Schema = make(map[string]string, len(t.Schema))
		for path, vt := range t.Schema {
			out.Schema[path] = string(vt)
		}
	}
	return out
}

func eventTypeValue(t *pb.EventType) eventtype.EventType {
	if t == nil {
		return eventtype.EventType{}
	}
	out := eventtype.EventType{
		ID: t.Id, Source: t.Source, Name: t.Name,
		CreatedAt: timeValue(t.CreatedAt), UpdatedAt: timeValue(t.UpdatedAt),
	}
	for _, h := range t.HeaderConditions {
		out.Rule.Headers = append(out.Rule.Headers, eventtype.HeaderCondition{Name: h.Name, Value: h.Value})
	}
	for _, p := range t.PayloadConditions {
		out.Rule.Payload = append(out.Rule.Payload, eventtype.PayloadCondition{Path: p.Path, Op: eventtype.Operator(p.Op), Value: p.Value})
	}
	if len(t.Schema) > 0 {
		out.Schema = make(map[string]eventtype.ValueType, len(t.Schema))
		for path, vt := range t.Schema {
			out.Schema[path] = eventtype.ValueType(vt)
		}
	}
	return out
}

// Server

func (s *server) eventTypes() (storecontract.EventTypeStore, error) {
	if s.deps.EventTypes == nil {
		return nil, errEventTypeUnavailable
	}
	return s.deps.EventTypes, nil
}

func (s *server) InsertEventType(ctx context.Context, r *pb.InsertEventTypeRequest) (*pb.InsertEventTypeResponse, error) {
	et, err := s.eventTypes()
	if err != nil {
		return nil, err
	}
	id, err := et.InsertEventType(ctx, eventTypeValue(r.EventType))
	if err != nil {
		return nil, s.logErr("InsertEventType", err)
	}
	return &pb.InsertEventTypeResponse{Id: id}, nil
}

func (s *server) UpdateEventType(ctx context.Context, r *pb.UpdateEventTypeRequest) (*pb.UpdateEventTypeResponse, error) {
	et, err := s.eventTypes()
	if err != nil {
		return nil, err
	}
	if err := et.UpdateEventType(ctx, eventTypeValue(r.EventType)); err != nil {
		return nil, s.logErr("UpdateEventType", err)
	}
	return &pb.UpdateEventTypeResponse{}, nil
}

func (s *server) DeleteEventType(ctx context.Context, r *pb.DeleteEventTypeRequest) (*pb.DeleteEventTypeResponse, error) {
	et, err := s.eventTypes()
	if err != nil {
		return nil, err
	}
	if err := et.DeleteEventType(ctx, r.Id); err != nil {
		return nil, s.logErr("DeleteEventType", err)
	}
	return &pb.DeleteEventTypeResponse{}, nil
}

func (s *server) ListEventTypes(ctx context.Context, _ *pb.ListEventTypesRequest) (*pb.ListEventTypesResponse, error) {
	et, err := s.eventTypes()
	if err != nil {
		return nil, err
	}
	types, err := et.ListEventTypes(ctx)
	if err != nil {
		return nil, s.logErr("ListEventTypes", err)
	}
	return &pb.ListEventTypesResponse{EventTypes: mapValues(types, eventTypeProto)}, nil
}

// Client

func (c *Client) InsertEventType(ctx context.Context, t eventtype.EventType) (string, error) {
	r, err := c.client.InsertEventType(ctx, &pb.InsertEventTypeRequest{EventType: eventTypeProto(t)})
	if err != nil {
		return "", unmapError(err)
	}
	return r.Id, nil
}

func (c *Client) UpdateEventType(ctx context.Context, t eventtype.EventType) error {
	_, err := c.client.UpdateEventType(ctx, &pb.UpdateEventTypeRequest{EventType: eventTypeProto(t)})
	return unmapError(err)
}

func (c *Client) DeleteEventType(ctx context.Context, id string) error {
	_, err := c.client.DeleteEventType(ctx, &pb.DeleteEventTypeRequest{Id: id})
	return unmapError(err)
}

func (c *Client) ListEventTypes(ctx context.Context) ([]eventtype.EventType, error) {
	r, err := c.client.ListEventTypes(ctx, &pb.ListEventTypesRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	return mapValues(r.EventTypes, eventTypeValue), nil
}
