package staterpc

import (
	"context"
	"encoding/hex"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/access"
	"github.com/samcharles93/archie-core/internal/domain/identity"
)

// tokenOwner is the identity a personal-token call acts for: the caller's
// principal, never a request field.
func (s *server) tokenOwner(ctx context.Context) (identity.IdentityID, error) {
	if s.deps.PersonalTokens == nil {
		return "", status.Error(codes.Unavailable, "personal tokens unavailable")
	}
	p, ok := access.PrincipalFromContext(ctx)
	if !ok || p.IdentityID == identity.SystemID {
		return "", identityStatus(fmt.Errorf("%w: personal tokens belong to a signed-in person", identity.ErrInvalid))
	}
	return p.IdentityID, nil
}

// tokenSubject validates a token ID: the hex SHA-256 the token binds as.
func tokenSubject(id string) (identity.Subject, error) {
	if raw, err := hex.DecodeString(id); err != nil || len(raw) != 32 {
		return identity.Subject{}, identityStatus(fmt.Errorf("%w: invalid token id", identity.ErrInvalid))
	}
	return identity.Subject{Issuer: identity.PersonalTokenIssuer, Subject: id}, nil
}

func tokenAudit(ctx context.Context, owner identity.IdentityID, id string) identity.Audit {
	return identity.Audit{ActorID: owner, Source: access.SourceFromContext(ctx), RequestID: "personal-token:" + id}
}

func (s *server) AddPersonalToken(ctx context.Context, r *pb.AddPersonalTokenRequest) (*pb.AddPersonalTokenResponse, error) {
	owner, err := s.tokenOwner(ctx)
	if err != nil {
		return nil, err
	}
	subject, err := tokenSubject(r.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.deps.PersonalTokens.BindSubject(ctx, owner, subject, tokenAudit(ctx, owner, r.GetId())); err != nil {
		return nil, identityStatus(err)
	}
	return &pb.AddPersonalTokenResponse{}, nil
}

func (s *server) ListPersonalTokens(ctx context.Context, _ *pb.ListPersonalTokensRequest) (*pb.ListPersonalTokensResponse, error) {
	owner, err := s.tokenOwner(ctx)
	if err != nil {
		return nil, err
	}
	tokens, err := s.deps.PersonalTokens.SubjectsOf(ctx, owner, identity.PersonalTokenIssuer)
	if err != nil {
		return nil, identityStatus(err)
	}
	out := make([]*pb.PersonalToken, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, &pb.PersonalToken{Id: t.ID, CreatedAt: timestamp(t.CreatedAt)})
	}
	return &pb.ListPersonalTokensResponse{Tokens: out}, nil
}

func (s *server) RevokePersonalToken(ctx context.Context, r *pb.RevokePersonalTokenRequest) (*pb.RevokePersonalTokenResponse, error) {
	owner, err := s.tokenOwner(ctx)
	if err != nil {
		return nil, err
	}
	subject, err := tokenSubject(r.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.deps.PersonalTokens.UnbindSubject(ctx, owner, subject, tokenAudit(ctx, owner, r.GetId())); err != nil {
		return nil, identityStatus(err)
	}
	return &pb.RevokePersonalTokenResponse{}, nil
}

func (c *Client) AddPersonalToken(ctx context.Context, subject identity.Subject) error {
	_, err := c.client.AddPersonalToken(ctx, &pb.AddPersonalTokenRequest{Id: subject.Subject})
	return identityClientError(err)
}

func (c *Client) ListPersonalTokens(ctx context.Context) ([]identity.PersonalToken, error) {
	r, err := c.client.ListPersonalTokens(ctx, &pb.ListPersonalTokensRequest{})
	if err != nil {
		return nil, identityClientError(err)
	}
	out := make([]identity.PersonalToken, 0, len(r.GetTokens()))
	for _, t := range r.GetTokens() {
		out = append(out, identity.PersonalToken{ID: t.GetId(), CreatedAt: timeValue(t.GetCreatedAt())})
	}
	return out, nil
}

func (c *Client) RevokePersonalToken(ctx context.Context, id string) error {
	_, err := c.client.RevokePersonalToken(ctx, &pb.RevokePersonalTokenRequest{Id: id})
	return identityClientError(err)
}
