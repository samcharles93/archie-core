// Package secretengine adapts a secret-engine extension, served over the
// secretengine.v1 gRPC surface, to the secret registry's Engine.
package secretengine

import (
	"context"
	"fmt"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretenginev1 "github.com/samcharles93/archie-core/internal/contracts/secretengine/v1"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
)

// Surface is the go-plugin surface name an engine binary serves.
const Surface = "secretengine"

// resolveTimeout bounds one resolution: a hung engine must not block secret
// resolution forever.
const resolveTimeout = 30 * time.Second

// Plugin is the go-plugin shim for the surface. Impl is set only by an engine
// binary serving it.
type Plugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl secretenginev1.SecretEngineServiceServer
}

func (p *Plugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	secretenginev1.RegisterSecretEngineServiceServer(s, p.Impl)
	return nil
}

func (*Plugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return secretenginev1.NewSecretEngineServiceClient(c), nil
}

// Engine resolves secrets through a running extension.
type Engine struct {
	name   string
	client secretenginev1.SecretEngineServiceClient
}

func (e *Engine) Name() string { return e.name }

// Resolve returns the value for key. A key the engine does not hold is an
// error, as it is for every engine.
func (e *Engine) Resolve(key string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	resp, err := e.client.Resolve(ctx, &secretenginev1.ResolveRequest{Key: key})
	if status.Code(err) == codes.NotFound {
		return "", fmt.Errorf("secret engine %q: key %q not found", e.name, key)
	}
	if err != nil {
		return "", fmt.Errorf("secret engine %q: %w", e.name, err)
	}
	return resp.GetValue(), nil
}

// Start launches the engine extension and configures it.
func Start(ctx context.Context, host *extension.Host, spec extension.Spec, settings map[string]string) (*Engine, error) {
	raw, err := host.Start(ctx, spec, Surface, &Plugin{})
	if err != nil {
		return nil, err
	}
	client, ok := raw.(secretenginev1.SecretEngineServiceClient)
	if !ok {
		host.Stop(spec.Name)
		return nil, fmt.Errorf("extension %q: unexpected client type %T", spec.Name, raw)
	}
	if _, err := client.Configure(ctx, &secretenginev1.ConfigureRequest{Settings: settings}); err != nil {
		host.Stop(spec.Name)
		return nil, fmt.Errorf("extension %q: configure: %w", spec.Name, err)
	}
	return &Engine{name: spec.Name, client: client}, nil
}
