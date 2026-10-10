// Package controlplane owns control-plane resource registration, dispatch, and validation.
package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

// The control-plane wire sentinels cross gRPC matched on (code, canonical
// message), so their definitions live with the contract in
// internal/infrastructure/controlplanerpc. These are the same values re-exported,
// so every existing caller keeps one name and errors.Is keeps matching identity.
var (
	ErrValidation      = controlplanerpc.ErrValidation
	ErrNotFound        = controlplanerpc.ErrNotFound
	ErrVersionConflict = controlplanerpc.ErrVersionConflict
	ErrUnavailable     = controlplanerpc.ErrUnavailable
)

// ResourceStore is the persistence the control plane requires. The composition
// root asserts the State Store against it, so this is the single definition of
// what a control-plane backing store must offer.
type ResourceStore interface {
	ListResources(context.Context) ([]storecontract.Resource, error)
	Resource(ctx context.Context, orgID, kind string) (storecontract.Resource, error)
	ResourceHistory(ctx context.Context, orgID, kind string, limit int) ([]storecontract.Resource, error)
	Audit(ctx context.Context, table string, keys []string, limit int) ([]storecontract.AuditEntry, error)
	PutResource(context.Context, storecontract.ResourceWrite) (storecontract.Resource, error)
}

// defaultHistoryLimit caps an unbounded request. A settings resource edited
// daily for a year still fits, and the dashboard pages rather than streams.
const defaultHistoryLimit = 200

type Server struct {
	pb.UnimplementedControlPlaneServiceServer
	store       ResourceStore
	definitions map[string]Definition
	ordered     []Definition
	steps       workflow.StepRegistry
}

// NewServer builds the control plane server the State Store serves. steps is
// the workflow step vocabulary the composition root registered; this is the
// validating side of the vocabulary archie-agent executes against in a
// matching build (see stepRegistry: a skewed deploy can still disagree).
func NewServer(resources ResourceStore, steps *workflow.Manager) (*Server, error) {
	registry, err := stepRegistry(steps)
	if err != nil {
		return nil, err
	}
	definitions := builtinDefinitions(registry, steps.Settings())
	byKind := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		byKind[definition.Kind] = definition
	}
	return &Server{store: resources, definitions: byKind, ordered: definitions, steps: registry}, nil
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
	resource, err := s.resource(ctx, requestOrg(ctx), request.Kind)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.QueryResponse{Resource: resourceProto(resource)}, nil
}

// History answers a resource's audit trail. The value of each revision comes
// back with it: restoring one is an ordinary replace of that value, so no
// rollback command of its own is needed.
func (s *Server) History(ctx context.Context, request *pb.HistoryRequest) (*pb.HistoryResponse, error) {
	if _, ok := s.definitions[request.GetKind()]; !ok {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	limit := int(request.GetLimit())
	if limit <= 0 || limit > defaultHistoryLimit {
		limit = defaultHistoryLimit
	}
	revisions, err := s.store.ResourceHistory(ctx, requestOrg(ctx), request.Kind, limit)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*pb.Revision, 0, len(revisions))
	for _, revision := range revisions {
		out = append(out, &pb.Revision{
			Version: revision.Version, ValueJson: revision.Value, Actor: revision.Actor,
			Source: revision.Source, RequestId: revision.RequestID, At: timestamp(revision.At),
		})
	}
	return &pb.HistoryResponse{Revisions: out}, nil
}

// defaultAuditLimit caps an audit read: a page shows recent changes, not the
// whole trail.
const defaultAuditLimit = 500

// resourcesAuditTable is the sys_audit table name resource writes record
// under.
const resourcesAuditTable = "resources"

func (s *Server) Audit(ctx context.Context, request *pb.AuditRequest) (*pb.AuditResponse, error) {
	orgID := requestOrg(ctx)
	keys := make([]string, 0, len(request.GetKinds()))
	kindOf := make(map[string]string, len(request.GetKinds()))
	for _, kind := range request.GetKinds() {
		// A slash would let a kind spell another org's record key.
		if kind == "" || strings.Contains(kind, "/") {
			return nil, status.Errorf(codes.InvalidArgument, "invalid kind %q", kind)
		}
		key := storecontract.ResourceAuditKey(orgID, kind)
		keys = append(keys, key)
		kindOf[key] = kind
	}
	if len(keys) == 0 {
		return nil, status.Error(codes.InvalidArgument, "kinds are required")
	}
	limit := int(request.GetLimit())
	if limit <= 0 || limit > defaultAuditLimit {
		limit = defaultAuditLimit
	}
	entries, err := s.store.Audit(ctx, resourcesAuditTable, keys, limit)
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*pb.AuditEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, &pb.AuditEntry{
			Id: entry.ID, Table: entry.Table, RecordKey: kindOf[entry.RecordKey], Field: entry.Field,
			OldValueJson: entry.OldValue, NewValueJson: entry.NewValue, Version: entry.Version,
			Actor: entry.Actor, Source: entry.Source, RequestId: entry.RequestID, At: timestamp(entry.At),
		})
	}
	return &pb.AuditResponse{Entries: out}, nil
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
	if err := s.checkOrgWrite(ctx, requestOrg(ctx), request.Kind, value); err != nil {
		return nil, mapError(err)
	}
	resource, err := s.store.PutResource(ctx, storecontract.ResourceWrite{OrgID: requestOrg(ctx), Kind: request.Kind, Value: value, Actor: request.Actor, Source: request.Source, RequestID: request.RequestId, ExpectedVersion: request.ExpectedVersion, At: time.Now().UTC()})
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
		resource, err := s.resource(stream.Context(), requestOrg(stream.Context()), request.Kind)
		if err == nil && resource.Version > version {
			if err := stream.Send(&pb.WatchResponse{Resource: resourceProto(resource)}); err != nil {
				return err
			}
			version = resource.Version
		} else if err != nil && !errors.Is(err, storecontract.ErrResourceNotFound) {
			return mapError(err)
		}
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
		}
	}
}

// resource seeds an org's workflow documents on first use, including orgs
// created after this process started. File-derived settings are never copied.
func (s *Server) resource(ctx context.Context, orgID, kind string) (storecontract.Resource, error) {
	resource, err := s.store.Resource(ctx, orgID, kind)
	if !errors.Is(err, storecontract.ErrResourceNotFound) || (kind != WorkflowDefinitionsKind && kind != WorkflowEnablementKind) {
		// Serve the current channel schema so stored settings remain editable.
		if err == nil && kind == ChannelSettingsKind {
			resource.Value, err = normalizeChannels(resource.Value)
		}
		return resource, err
	}
	if _, err := s.seedKind(ctx, orgID, s.definitions[kind], config.Config{}); err != nil && !errors.Is(err, storecontract.ErrResourceVersionConflict) {
		return storecontract.Resource{}, err
	}
	return s.store.Resource(ctx, orgID, kind)
}

func resourceProto(resource storecontract.Resource) *pb.Resource {
	return &pb.Resource{OrgId: resource.OrgID, Kind: resource.Kind, Version: resource.Version, ValueJson: resource.Value, UpdatedAt: timestamp(resource.At)}
}

// requestOrg is the org a request acts in, derived from the caller's
// principal when the call carried one (rpcidentity.Callers).
func requestOrg(ctx context.Context) string {
	return string(org.OrgFromContext(ctx))
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
	case errors.Is(err, storecontract.ErrResourceNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, storecontract.ErrResourceVersionConflict):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(codes.Unavailable, err.Error())
	}
}

// checkModelAliases refuses workflow definitions whose agent steps name a
// model alias the writing org cannot resolve.
func (s *Server) checkModelAliases(ctx context.Context, orgID string, value []byte) error {
	models, err := s.ModelsFor(ctx, orgID)
	if err != nil {
		return err
	}
	var collection workflow.WorkflowDefinitionCollection
	if err := json.Unmarshal(value, &collection); err != nil {
		return err
	}
	if err := workflow.CheckModelAliases(collection, s.steps, models.Aliases); err != nil {
		return fmt.Errorf("%w: %w", ErrValidation, err)
	}
	return nil
}
