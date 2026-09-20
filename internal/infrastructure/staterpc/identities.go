package staterpc

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

func (s *server) ListIdentities(ctx context.Context, _ *pb.ListIdentitiesRequest) (*pb.ListIdentitiesResponse, error) {
	if s.deps.Identities == nil {
		return nil, status.Error(codes.Unavailable, "identity store unavailable")
	}
	values, err := s.deps.Identities.List(ctx)
	if err != nil {
		return nil, identityStatus(err)
	}
	out := make([]*pb.Identity, len(values))
	for i := range values {
		out[i] = identityProto(values[i])
	}
	return &pb.ListIdentitiesResponse{Identities: out}, nil
}

func (s *server) GetIdentity(ctx context.Context, request *pb.GetIdentityRequest) (*pb.GetIdentityResponse, error) {
	value, err := s.deps.Identities.Get(ctx, identity.IdentityID(request.Id))
	if errors.Is(err, identity.ErrNotFound) {
		value, err = s.deps.Identities.ResolveLegacyName(ctx, request.Id)
	}
	if err != nil {
		return nil, identityStatus(err)
	}
	return &pb.GetIdentityResponse{Identity: identityProto(value)}, nil
}

func (s *server) CreateIdentity(ctx context.Context, request *pb.CreateIdentityRequest) (*pb.CreateIdentityResponse, error) {
	value, err := identity.New(identity.IdentityID(request.Id), identity.Kind(request.Kind), request.DisplayName)
	if err == nil {
		value, err = s.deps.Identities.Create(ctx, value, identityAudit(request.Audit))
	}
	if err != nil {
		return nil, identityStatus(err)
	}
	return &pb.CreateIdentityResponse{Identity: identityProto(value)}, nil
}

func (s *server) RenameIdentity(ctx context.Context, request *pb.RenameIdentityRequest) (*pb.RenameIdentityResponse, error) {
	value, err := s.applyIdentityCommand(ctx, request.Id, request.ExpectedVersion, request.Audit, identity.Command{Type: identity.CommandRename, DisplayName: request.DisplayName})
	return &pb.RenameIdentityResponse{Identity: identityProto(value)}, err
}

func (s *server) SuspendIdentity(ctx context.Context, request *pb.SuspendIdentityRequest) (*pb.SuspendIdentityResponse, error) {
	value, err := s.applyIdentityCommand(ctx, request.Id, request.ExpectedVersion, request.Audit, identity.Command{Type: identity.CommandSuspend})
	return &pb.SuspendIdentityResponse{Identity: identityProto(value)}, err
}

func (s *server) ReactivateIdentity(ctx context.Context, request *pb.ReactivateIdentityRequest) (*pb.ReactivateIdentityResponse, error) {
	value, err := s.applyIdentityCommand(ctx, request.Id, request.ExpectedVersion, request.Audit, identity.Command{Type: identity.CommandReactivate})
	return &pb.ReactivateIdentityResponse{Identity: identityProto(value)}, err
}

func (s *server) RetireIdentity(ctx context.Context, request *pb.RetireIdentityRequest) (*pb.RetireIdentityResponse, error) {
	value, err := s.applyIdentityCommand(ctx, request.Id, request.ExpectedVersion, request.Audit, identity.Command{Type: identity.CommandRetire})
	return &pb.RetireIdentityResponse{Identity: identityProto(value)}, err
}

func (s *server) applyIdentityCommand(ctx context.Context, id string, version int64, audit *pb.IdentityAudit, command identity.Command) (identity.Identity, error) {
	value, err := s.deps.Identities.Apply(ctx, identity.IdentityID(id), version, command, identityAudit(audit))
	if err != nil {
		return identity.Identity{}, identityStatus(err)
	}
	return value, nil
}

func (c *Client) List(ctx context.Context) ([]identity.Identity, error) {
	response, err := c.client.ListIdentities(ctx, &pb.ListIdentitiesRequest{})
	if err != nil {
		return nil, identityClientError(err)
	}
	out := make([]identity.Identity, len(response.Identities))
	for i := range response.Identities {
		out[i] = identityValue(response.Identities[i])
	}
	return out, nil
}

func (c *Client) Get(ctx context.Context, id identity.IdentityID) (identity.Identity, error) {
	response, err := c.client.GetIdentity(ctx, &pb.GetIdentityRequest{Id: string(id)})
	if err != nil {
		return identity.Identity{}, identityClientError(err)
	}
	return identityValue(response.Identity), nil
}

func (c *Client) ResolveLegacyName(ctx context.Context, name string) (identity.Identity, error) {
	return c.Get(ctx, identity.IdentityID(name))
}

func (c *Client) Create(ctx context.Context, value identity.Identity, audit identity.Audit) (identity.Identity, error) {
	response, err := c.client.CreateIdentity(ctx, &pb.CreateIdentityRequest{Id: string(value.ID), Kind: string(value.Kind), DisplayName: value.DisplayName, Audit: auditProto(audit)})
	if err != nil {
		return identity.Identity{}, identityClientError(err)
	}
	return identityValue(response.Identity), nil
}

func (c *Client) Apply(ctx context.Context, id identity.IdentityID, version int64, command identity.Command, audit identity.Audit) (identity.Identity, error) {
	var value *pb.Identity
	var err error
	switch command.Type {
	case identity.CommandRename:
		response, callErr := c.client.RenameIdentity(ctx, &pb.RenameIdentityRequest{Id: string(id), ExpectedVersion: version, DisplayName: command.DisplayName, Audit: auditProto(audit)})
		err = callErr
		if response != nil {
			value = response.Identity
		}
	case identity.CommandSuspend:
		response, callErr := c.client.SuspendIdentity(ctx, &pb.SuspendIdentityRequest{Id: string(id), ExpectedVersion: version, Audit: auditProto(audit)})
		err = callErr
		if response != nil {
			value = response.Identity
		}
	case identity.CommandReactivate:
		response, callErr := c.client.ReactivateIdentity(ctx, &pb.ReactivateIdentityRequest{Id: string(id), ExpectedVersion: version, Audit: auditProto(audit)})
		err = callErr
		if response != nil {
			value = response.Identity
		}
	case identity.CommandRetire:
		response, callErr := c.client.RetireIdentity(ctx, &pb.RetireIdentityRequest{Id: string(id), ExpectedVersion: version, Audit: auditProto(audit)})
		err = callErr
		if response != nil {
			value = response.Identity
		}
	default:
		return identity.Identity{}, identity.ErrInvalid
	}
	if err != nil {
		return identity.Identity{}, identityClientError(err)
	}
	return identityValue(value), nil
}

func identityProto(value identity.Identity) *pb.Identity {
	return &pb.Identity{Id: string(value.ID), Kind: string(value.Kind), DisplayName: value.DisplayName, Lifecycle: string(value.Lifecycle), Version: value.Version, CreatedAt: timestamppb.New(value.CreatedAt), UpdatedAt: timestamppb.New(value.UpdatedAt)}
}

func identityValue(value *pb.Identity) identity.Identity {
	if value == nil {
		return identity.Identity{}
	}
	return identity.Identity{ID: identity.IdentityID(value.Id), Kind: identity.Kind(value.Kind), DisplayName: value.DisplayName, Lifecycle: identity.Lifecycle(value.Lifecycle), Version: value.Version, CreatedAt: value.CreatedAt.AsTime(), UpdatedAt: value.UpdatedAt.AsTime()}
}

func identityAudit(value *pb.IdentityAudit) identity.Audit {
	if value == nil {
		return identity.Audit{}
	}
	return identity.Audit{ActorID: identity.IdentityID(value.ActorId), Source: value.Source, RequestID: value.RequestId}
}

func auditProto(value identity.Audit) *pb.IdentityAudit {
	return &pb.IdentityAudit{ActorId: string(value.ActorID), Source: value.Source, RequestId: value.RequestID}
}

func identityStatus(err error) error {
	switch {
	case errors.Is(err, identity.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, identity.ErrConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, identity.ErrInvalid), errors.Is(err, identity.ErrIllegalTransition), errors.Is(err, identity.ErrSystemImmutable):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, "internal state store error")
	}
}

func identityClientError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return identity.ErrNotFound
	case codes.Aborted:
		return identity.ErrConflict
	case codes.FailedPrecondition:
		return errors.Join(identity.ErrInvalid, err)
	default:
		return err
	}
}
