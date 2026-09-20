// Package controlplane owns control-plane resource registration, dispatch, and validation.
package controlplane

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/store"
)

var (
	ErrValidation      = errors.New("control-plane validation")
	ErrNotFound        = errors.New("control-plane resource not found")
	ErrVersionConflict = errors.New("control-plane version conflict")
	ErrUnavailable     = errors.New("control-plane unavailable")
)

type resourceStore interface {
	Resource(context.Context, string) (store.Resource, error)
	PutResource(context.Context, store.ResourceWrite) (store.Resource, error)
}

type Server struct {
	pb.UnimplementedControlPlaneServiceServer
	store       resourceStore
	definitions map[string]Definition
	ordered     []Definition
}

func NewServer(resources resourceStore) *Server {
	definitions := builtinDefinitions()
	byKind := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byKind[definition.Kind] = definition
	}
	return &Server{store: resources, definitions: byKind, ordered: definitions}
}

func (s *Server) Catalog(context.Context, *pb.CatalogRequest) (*pb.CatalogResponse, error) {
	descriptors := make([]*pb.ResourceDescriptor, 0, len(s.ordered)+len(domainManagedDescriptors()))
	for _, definition := range s.ordered {
		descriptors = append(descriptors, definition.Descriptor())
	}
	descriptors = append(descriptors, domainManagedDescriptors()...)
	return &pb.CatalogResponse{Resources: descriptors}, nil
}

func (s *Server) Query(ctx context.Context, request *pb.QueryRequest) (*pb.QueryResponse, error) {
	if _, ok := s.definitions[request.GetKind()]; !ok {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	resource, err := s.store.Resource(ctx, request.Kind)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.QueryResponse{Resource: resourceProto(resource)}, nil
}

func (s *Server) Command(ctx context.Context, request *pb.CommandRequest) (*pb.CommandResponse, error) {
	definition, ok := s.definitions[request.GetKind()]
	if !ok || request.GetCommand() != "replace" {
		return nil, status.Error(codes.NotFound, "resource or command not found")
	}
	if request.GetActor() == "" || request.GetSource() == "" || request.GetRequestId() == "" {
		return nil, status.Error(codes.InvalidArgument, "actor, source, and request ID are required")
	}
	value, err := definition.Decode(request.ValueJson)
	if err != nil {
		return nil, mapError(err)
	}
	resource, err := s.store.PutResource(ctx, store.ResourceWrite{Kind: request.Kind, Value: value, Actor: request.Actor, Source: request.Source, RequestID: request.RequestId, ExpectedVersion: request.ExpectedVersion, At: time.Now().UTC()})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CommandResponse{Resource: resourceProto(resource)}, nil
}

func (s *Server) Watch(request *pb.WatchRequest, stream pb.ControlPlaneService_WatchServer) error {
	if _, ok := s.definitions[request.GetKind()]; !ok {
		return status.Error(codes.NotFound, "resource not found")
	}
	version := request.AfterVersion
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		resource, err := s.store.Resource(stream.Context(), request.Kind)
		if err == nil && resource.Version > version {
			if err := stream.Send(&pb.WatchResponse{Resource: resourceProto(resource)}); err != nil {
				return err
			}
			version = resource.Version
		} else if err != nil && !errors.Is(err, store.ErrResourceNotFound) {
			return mapError(err)
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

func resourceProto(resource store.Resource) *pb.Resource {
	return &pb.Resource{Kind: resource.Kind, Version: resource.Version, ValueJson: resource.Value, UpdatedAt: timestamp(resource.At)}
}

func timestamp(value time.Time) *timestamppb.Timestamp {
	if value.IsZero() {
		return nil
	}
	return timestamppb.New(value)
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, store.ErrResourceNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrResourceVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(codes.Unavailable, err.Error())
	}
}
