package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

// Client is the control-plane client for every resource that does not resolve
// a workflow step type: catalog, history, settings, personas, schedules. It
// carries no step vocabulary, so a process that never reads or writes a
// workflow definition -- archie-messaging, which only loads channel settings,
// and archie-gateway, which reads catalog, runtime settings and personas --
// needs no provider set and cannot be failed by one.
// Client is the control-plane client a process that serves the store uses. It
// embeds the Messaging Service's client -- the generic resource read path and
// the channel-settings projection -- and adds the resource-specific reads and
// watches, which need the workflow and cronstore engines that such a process
// already links (archie-core-1ng1).
type Client struct {
	*controlplanerpc.Client
	rpc pb.ControlPlaneServiceClient
}

// NewClient dials the control plane over an existing connection.
func NewClient(conn grpc.ClientConnInterface) *Client {
	return &Client{rpc: pb.NewControlPlaneServiceClient(conn)}
}

// NewRPCClient wraps an already-constructed generated client.
func NewRPCClient(client pb.ControlPlaneServiceClient) *Client {
	return &Client{Client: controlplanerpc.NewRPCClient(client), rpc: client}
}

// WorkflowDefinitionsClient reads and replaces the workflow-definitions
// resource, the one control-plane surface whose stored values name workflow
// step types. Resolving those names needs the step vocabulary the process
// registered at its composition root, so the vocabulary is a constructor
// dependency of this surface and of no other: a caller that cannot reach
// workflow definitions cannot ask for one.
type WorkflowDefinitionsClient struct {
	rpc   pb.ControlPlaneServiceClient
	steps workflow.StepRegistry
}

func NewWorkflowDefinitionsClient(client pb.ControlPlaneServiceClient, steps *workflow.Manager) (*WorkflowDefinitionsClient, error) {
	registry, err := stepRegistry(steps)
	if err != nil {
		return nil, err
	}
	return &WorkflowDefinitionsClient{rpc: client, steps: registry}, nil
}

func (c *Client) Catalog(ctx context.Context) ([]*pb.ResourceDescriptor, error) {
	response, err := c.rpc.Catalog(ctx, &pb.CatalogRequest{})
	if err != nil {
		return nil, controlplanerpc.ClientError(err)
	}
	return response.Resources, nil
}

func (c *WorkflowDefinitionsClient) WorkflowDefinitions(ctx context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: WorkflowDefinitionsKind})
	if err != nil {
		return workflow.WorkflowDefinitionCollection{}, 0, controlplanerpc.ClientError(err)
	}
	definitions, err := workflow.DecodeDefinitionCollection(response.Resource.ValueJson, c.steps)
	return definitions, response.Resource.Version, err
}

// ReplaceWorkflowDefinitions replaces the collection. Passing
// workflow.ShippedDefinitions() restores all shipped definitions while keeping
// the previous override in resource history.
//
// No production process calls it today: the daemon's only consumer of this
// surface reads (internal/daemon's WorkflowDefinitions interface), so this half
// is reached by tests until a writer exists.
func (c *WorkflowDefinitionsClient) ReplaceWorkflowDefinitions(ctx context.Context, definitions workflow.WorkflowDefinitionCollection, expectedVersion int64, actor, source, requestID string) (int64, error) {
	value, err := encodeWorkflowDefinitions(definitions, c.steps)
	if err != nil {
		return 0, err
	}
	response, err := c.rpc.Command(ctx, &pb.CommandRequest{Kind: WorkflowDefinitionsKind, Command: "replace", ValueJson: value, ExpectedVersion: expectedVersion, Actor: actor, Source: source, RequestId: requestID})
	if err != nil {
		return 0, controlplanerpc.ClientError(err)
	}
	return response.Resource.Version, nil
}

func (c *Client) WorkflowExecutionSettings(ctx context.Context) (workflow.ExecutionSettings, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: WorkflowExecutionSettingsKind})
	if err != nil {
		return workflow.ExecutionSettings{}, 0, controlplanerpc.ClientError(err)
	}
	settings, err := decodeSettings(response.Resource.ValueJson)
	return settings, response.Resource.Version, err
}

func (c *Client) ReplaceWorkflowExecutionSettings(ctx context.Context, settings workflow.ExecutionSettings, expectedVersion int64, actor, source, requestID string) (int64, error) {
	if err := settings.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrValidation, err)
	}
	value, err := encodeSettings(settings)
	if err != nil {
		return 0, err
	}
	response, err := c.rpc.Command(ctx, &pb.CommandRequest{Kind: WorkflowExecutionSettingsKind, Command: "replace", ValueJson: value, ExpectedVersion: expectedVersion, Actor: actor, Source: source, RequestId: requestID})
	if err != nil {
		return 0, controlplanerpc.ClientError(err)
	}
	return response.Resource.Version, nil
}

func (c *Client) WatchWorkflowExecutionSettings(ctx context.Context, afterVersion int64) (<-chan AppliedSettings, error) {
	stream, err := c.rpc.Watch(ctx, &pb.WatchRequest{Kind: WorkflowExecutionSettingsKind, AfterVersion: afterVersion})
	if err != nil {
		return nil, controlplanerpc.ClientError(err)
	}
	return watchUpdates(ctx, stream,
		func(resource *pb.Resource) AppliedSettings {
			settings, err := decodeSettings(resource.ValueJson)
			return AppliedSettings{Settings: settings, Version: resource.Version, Err: err}
		},
		func(err error) AppliedSettings { return AppliedSettings{Err: err} }), nil
}

type AppliedSettings struct {
	Settings workflow.ExecutionSettings
	Version  int64
	Err      error
}

func (c *Client) Personas(ctx context.Context) (agent.PersonaCollection, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: PersonasKind})
	if err != nil {
		return agent.PersonaCollection{}, 0, controlplanerpc.ClientError(err)
	}
	collection, err := decodePersonas(response.Resource.ValueJson)
	return collection, response.Resource.Version, err
}

type AppliedPersonas struct {
	Collection agent.PersonaCollection
	Version    int64
	Err        error
}

func (c *Client) WatchPersonas(ctx context.Context, afterVersion int64) (<-chan AppliedPersonas, error) {
	stream, err := c.rpc.Watch(ctx, &pb.WatchRequest{Kind: PersonasKind, AfterVersion: afterVersion})
	if err != nil {
		return nil, controlplanerpc.ClientError(err)
	}
	return watchUpdates(ctx, stream,
		func(resource *pb.Resource) AppliedPersonas {
			collection, err := decodePersonas(resource.ValueJson)
			return AppliedPersonas{Collection: collection, Version: resource.Version, Err: err}
		},
		func(err error) AppliedPersonas { return AppliedPersonas{Err: err} }), nil
}

// watchUpdates turns one Watch stream into the channel a watch loop reads: every
// document the stream carries, decoded, and then a single update carrying the
// error that ended it -- unless the stream ended cleanly or the context did, in
// which case the channel just closes.
func watchUpdates[T any](
	ctx context.Context,
	stream grpc.ServerStreamingClient[pb.WatchResponse],
	update func(resource *pb.Resource) T,
	failed func(err error) T,
) <-chan T {
	out := make(chan T)
	go func() {
		defer close(out)
		for {
			response, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					sendUpdate(ctx, out, failed(controlplanerpc.ClientError(err)))
				}
				return
			}
			if !sendUpdate(ctx, out, update(response.Resource)) {
				return
			}
		}
	}()
	return out
}

// sendUpdate hands update to the reader of one Watch stream, reporting false
// when ctx ended first. A reader leaves without draining what is left for it:
// the watch that reads these streams returns on cancellation, so a bare send
// parks this producer -- and the stream it holds -- for the rest of the
// process's life, one goroutine and one channel per kind at every exit. That
// coupling is load-bearing: the reader's prompt shutdown rests on this side
// stopping on the same context, and neither half is safe alone.
func sendUpdate[T any](ctx context.Context, out chan<- T, update T) bool {
	select {
	case out <- update:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Client) Schedules(ctx context.Context) ([]cronstore.JobSpec, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: SchedulesKind})
	if err != nil {
		return nil, 0, controlplanerpc.ClientError(err)
	}
	var jobs []cronstore.JobSpec
	if err := json.Unmarshal(response.Resource.ValueJson, &jobs); err != nil {
		return nil, 0, err
	}
	return jobs, response.Resource.Version, nil
}

func (c *Client) ReplaceSchedules(ctx context.Context, jobs []cronstore.JobSpec, expectedVersion int64, actor, source, requestID string) (int64, error) {
	value, err := json.Marshal(jobs)
	if err != nil {
		return 0, err
	}
	response, err := c.rpc.Command(ctx, &pb.CommandRequest{Kind: SchedulesKind, Command: "replace", ValueJson: value, ExpectedVersion: expectedVersion, Actor: actor, Source: source, RequestId: requestID})
	if err != nil {
		return 0, controlplanerpc.ClientError(err)
	}
	return response.Resource.Version, nil
}
