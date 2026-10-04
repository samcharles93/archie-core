// Package controlplanerpc is the control-plane resource client and the
// channel-settings document.
package controlplanerpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

// ChannelSettingsKind is the control-plane resource kind holding the channel
// settings document. It is declared here because this package owns the document
// and the client names the kind; internal/app/controlplane re-exports it, so the
// store-backed side and its callers keep one name for one kind.
const ChannelSettingsKind = "channel-settings"

var (
	ErrValidation      = errors.New("control-plane validation")
	ErrNotFound        = errors.New("control-plane resource not found")
	ErrVersionConflict = errors.New("control-plane version conflict")
	ErrUnavailable     = errors.New("control-plane unavailable")
)

// ResourceStore is the persistence the control plane requires. The composition
// root asserts the State Store against it, so this is the single definition of
// what a control-plane backing store must offer.

type Client struct{ rpc pb.ControlPlaneServiceClient }

func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{rpc: pb.NewControlPlaneServiceClient(conn)}
}

func NewRPCClient(client pb.ControlPlaneServiceClient) *Client { return &Client{rpc: client} }

// ResourceReader is the read side of the control-plane client: one resource
// query per kind. The store-backed server implements it too, which is why the
// layering functions here are shared with internal/app/controlplane.
type ResourceReader interface {
	Query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error)
}

func (c *Client) Query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	response, err := c.rpc.Query(ctx, QueryRequest(ctx, kind))
	if err != nil {
		mapped := ClientError(err)
		if errors.Is(mapped, ErrNotFound) {
			return 0, false, nil
		}
		return 0, false, mapped
	}
	if response.Resource == nil {
		return 0, false, fmt.Errorf("%s resource missing", kind)
	}
	if err := decode(response.Resource.ValueJson); err != nil {
		return 0, false, fmt.Errorf("decode %s: %w", kind, err)
	}
	return response.Resource.Version, true, nil
}

// RuntimeChatConfig layers the stored channel settings over the file document's
// chat section.
func (c *Client) RuntimeChatConfig(ctx context.Context, base config.ChatConfig) (config.ChatConfig, int64, error) {
	return RuntimeChatConfigFrom(ctx, c, base)
}

func RuntimeChatConfigFrom(ctx context.Context, reader ResourceReader, base config.ChatConfig) (config.ChatConfig, int64, error) {
	out := base
	version, found, err := reader.Query(ctx, ChannelSettingsKind, func(value []byte) error {
		var settings ChannelSettings
		if err := json.Unmarshal(value, &settings); err != nil {
			return err
		}
		check, install := base.Telegram.UpdateCheckCommand, base.Telegram.UpdateInstallCommand
		out = config.ChatConfig{
			Operator: settings.Operator, ShowToolCalls: settings.ShowToolCalls, MaxSteps: settings.MaxSteps,
			Models:                 settings.Models,
			Telegram:               config.TelegramConfig{AllowedUserIDs: settings.Telegram.AllowedUserIDs, Token: settings.Telegram.Token, UpdateCheckCommand: check, UpdateInstallCommand: install},
			RateLimit:              config.RateLimitConfig{Window: time.Duration(settings.RateLimit.Window), MaxRequests: settings.RateLimit.MaxRequests},
			UnrestrictedFilesystem: settings.UnrestrictedFilesystem, Workspace: settings.Workspace,
		}
		return nil
	})
	if err != nil {
		return out, 0, err
	}
	if !found {
		return out, 0, nil
	}
	return out, version, nil
}

// ClientError maps a gRPC status onto the wire sentinels both sides match on.
// It is exported because the store-backed server's own client methods need the
// same mapping, and a second copy would be a second contract.
func ClientError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument:
		return fmt.Errorf("%w: %w", ErrValidation, err)
	case codes.NotFound:
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	case codes.Aborted:
		return fmt.Errorf("%w: %w", ErrVersionConflict, err)
	case codes.Unavailable:
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	default:
		return err
	}
}
