// Package controlplanerpc is the Messaging Service side of the control-plane
// contract: the generic resource client, the channel-settings document it
// projects, and the wire sentinels both sides match on. It exists so a service
// that only reads stored settings does not link the store-backed server, the
// workflow engine or the SQLite driver (archie-core-1ng1).
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

// WorkflowDefinitionsClient reads and replaces the workflow-definitions
// resource, the one control-plane surface whose stored values name workflow
// step types. Resolving those names needs the step vocabulary the process
// registered at its composition root, so the vocabulary is a constructor
// dependency of this surface and of no other: a caller that cannot reach
// workflow definitions cannot ask for one.

// ResourceReader is the read side of the control-plane client: one resource
// query per kind. The store-backed server implements it too, which is why the
// layering functions here are shared with internal/app/controlplane.
type ResourceReader interface {
	Query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error)
}

func (c *Client) Query(ctx context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: kind})
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
			Models: settings.Models, Email: config.EmailConfig{ListenAddr: settings.Email.ListenAddr, RelayAddr: settings.Email.RelayAddr}, WebhookAddr: settings.WebhookAddr,
			Webhook:                config.WebhookRoute{Path: settings.Webhook.Path, Secret: settings.Webhook.Secret, Template: settings.Webhook.Template, DeliverTo: settings.Webhook.DeliverTo},
			Telegram:               config.TelegramConfig{AllowedUserIDs: settings.Telegram.AllowedUserIDs, Token: settings.Telegram.Token, TokenEnv: settings.Telegram.TokenEnv, UpdateCheckCommand: check, UpdateInstallCommand: install},
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

// layerResource decodes a resource and records the version it came from, so the
// layering ends up holding the version of every kind it applied.
//
// A kind with no stored value is not an error and records no version: the file
// document's value stays in effect. That is the state a seed the resource
// validator refused leaves behind (controlplane.Server.ImportConfig skips it),
// and failing here instead would stop the process with a database-named error
// that editing config.toml cannot clear.

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
