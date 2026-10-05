// Package channelext adapts a chat channel extension, served over the
// channel.v1 gRPC surface, to the messaging service's channels.Channel. The
// host serves the gateway ChatService back to the extension over the plugin
// broker, so the extension never holds a gateway address or credential.
package channelext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	channelv1 "github.com/samcharles93/archie-core/internal/contracts/channel/v1"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/domain/taskactions"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/infrastructure/gatewayrpc"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// Surface is the go-plugin surface name a channel binary serves.
const Surface = "channel"

// Plugin is the go-plugin shim for the surface. Impl is set only by a channel
// binary serving it.
type Plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl channelv1.ChannelServiceServer
}

func (p *Plugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	channelv1.RegisterChannelServiceServer(s, p.Impl)
	return nil
}

type client struct {
	rpc    channelv1.ChannelServiceClient
	broker *goplugin.GRPCBroker
}

func (*Plugin) GRPCClient(_ context.Context, broker *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &client{rpc: channelv1.NewChannelServiceClient(c), broker: broker}, nil
}

// Channel is one channel extension. It satisfies channels.Channel.
type Channel struct {
	name     string
	host     *extension.Host
	spec     extension.Spec
	settings map[string]string
	secrets  map[string]string
}

// New returns a Channel that launches spec on Start. secrets are the resolved
// values of the settings' _ref entries.
func New(host *extension.Host, name string, spec extension.Spec, settings, secrets map[string]string) *Channel {
	return &Channel{name: name, host: host, spec: spec, settings: settings, secrets: secrets}
}

func (c *Channel) Name() string { return c.name }

// Start launches the extension and blocks until it stops. The process dies
// with ctx.
func (c *Channel) Start(ctx context.Context, chat messaging.ChatContract, lifecycle messaging.Lifecycle) error {
	lifecycle.ReportStarting()
	raw, err := c.host.Start(ctx, c.spec, Surface, &Plugin{})
	if err != nil {
		return err
	}
	defer c.host.Stop(c.spec.Name)
	ext, ok := raw.(*client)
	if !ok {
		return fmt.Errorf("extension %q: unexpected client type %T", c.spec.Name, raw)
	}

	id := ext.broker.NextId()
	go ext.broker.AcceptAndServe(id, func(opts []grpc.ServerOption) *grpc.Server {
		server := grpc.NewServer(opts...)
		gatewayrpc.RegisterServer(server, restricted{chat}, nil, gatewayrpc.Catalog{})
		return server
	})

	stream, err := ext.rpc.Run(ctx, &channelv1.RunRequest{Settings: c.settings, Secrets: c.secrets, ChatBrokerId: id})
	if err != nil {
		return fmt.Errorf("extension %q: run: %w", c.spec.Name, err)
	}
	for {
		event, err := stream.Recv()
		switch {
		case errors.Is(err, io.EOF):
			return nil
		case ctx.Err() != nil:
			return nil
		case err != nil:
			return fmt.Errorf("extension %q: %w", c.spec.Name, err)
		}
		switch event.GetState() {
		case channelv1.RunResponse_STATE_STARTING:
			lifecycle.ReportStarting()
		case channelv1.RunResponse_STATE_RUNNING:
			lifecycle.ReportRunning()
		}
	}
}

func (c *Channel) Stop(context.Context) error {
	c.host.Stop(c.spec.Name)
	return nil
}

// ConfigSchema is empty: an extension validates its own settings when it runs.
func (*Channel) ConfigSchema() json.RawMessage { return json.RawMessage(`{}`) }

func (*Channel) ValidateConfig(map[string]any) error { return nil }

// restricted is the chat surface a channel extension may use. A channel
// carries conversations; mutating tasks as an operator across identities is
// the dashboard's authority, not a channel's.
type restricted struct{ messaging.ChatContract }

func (restricted) ApplyOperatorTaskAction(context.Context, taskactions.Actor, int64, taskstate.Action, taskactions.ActionPayload) (messaging.TaskActionResult, error) {
	return messaging.TaskActionResult{}, status.Error(codes.PermissionDenied, "a channel extension cannot act as an operator")
}
