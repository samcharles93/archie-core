package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/agent"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/cronstore"
)

type Client struct {
	rpc pb.ControlPlaneServiceClient
	// steps is the step vocabulary this client validates and decodes workflow
	// definitions against, resolved once by stepRegistry.
	steps workflow.StepRegistry
}

func NewClient(conn grpc.ClientConnInterface, steps *workflow.Manager) *Client {
	return &Client{rpc: pb.NewControlPlaneServiceClient(conn), steps: stepRegistry(steps)}
}

func NewRPCClient(client pb.ControlPlaneServiceClient, steps *workflow.Manager) *Client {
	return &Client{rpc: client, steps: stepRegistry(steps)}
}

func (c *Client) Catalog(ctx context.Context) ([]*pb.ResourceDescriptor, error) {
	response, err := c.rpc.Catalog(ctx, &pb.CatalogRequest{})
	if err != nil {
		return nil, clientError(err)
	}
	return response.Resources, nil
}

func (c *Client) WorkflowDefinitions(ctx context.Context) (workflow.WorkflowDefinitionCollection, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: WorkflowDefinitionsKind})
	if err != nil {
		return workflow.WorkflowDefinitionCollection{}, 0, clientError(err)
	}
	definitions, err := workflow.DecodeDefinitionCollection(response.Resource.ValueJson, c.steps)
	return definitions, response.Resource.Version, err
}

// ReplaceWorkflowDefinitions replaces the collection. Passing
// workflow.ShippedDefinitions() restores all shipped definitions while keeping
// the previous override in resource history.
func (c *Client) ReplaceWorkflowDefinitions(ctx context.Context, definitions workflow.WorkflowDefinitionCollection, expectedVersion int64, actor, source, requestID string) (int64, error) {
	value, err := encodeWorkflowDefinitions(definitions, c.steps)
	if err != nil {
		return 0, err
	}
	response, err := c.rpc.Command(ctx, &pb.CommandRequest{Kind: WorkflowDefinitionsKind, Command: "replace", ValueJson: value, ExpectedVersion: expectedVersion, Actor: actor, Source: source, RequestId: requestID})
	if err != nil {
		return 0, clientError(err)
	}
	return response.Resource.Version, nil
}

func (c *Client) WorkflowExecutionSettings(ctx context.Context) (workflow.ExecutionSettings, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: WorkflowExecutionSettingsKind})
	if err != nil {
		return workflow.ExecutionSettings{}, 0, clientError(err)
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
		return 0, clientError(err)
	}
	return response.Resource.Version, nil
}

func (c *Client) WatchWorkflowExecutionSettings(ctx context.Context, afterVersion int64) (<-chan AppliedSettings, error) {
	stream, err := c.rpc.Watch(ctx, &pb.WatchRequest{Kind: WorkflowExecutionSettingsKind, AfterVersion: afterVersion})
	if err != nil {
		return nil, clientError(err)
	}
	out := make(chan AppliedSettings)
	go func() {
		defer close(out)
		for {
			response, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					out <- AppliedSettings{Err: clientError(err)}
				}
				return
			}
			settings, err := decodeSettings(response.Resource.ValueJson)
			out <- AppliedSettings{Settings: settings, Version: response.Resource.Version, Err: err}
		}
	}()
	return out, nil
}

type AppliedSettings struct {
	Settings workflow.ExecutionSettings
	Version  int64
	Err      error
}

func (c *Client) Personas(ctx context.Context) (agent.PersonaCollection, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: PersonasKind})
	if err != nil {
		return agent.PersonaCollection{}, 0, clientError(err)
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
		return nil, clientError(err)
	}
	out := make(chan AppliedPersonas)
	go func() {
		defer close(out)
		for {
			response, err := stream.Recv()
			if err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					out <- AppliedPersonas{Err: clientError(err)}
				}
				return
			}
			collection, err := decodePersonas(response.Resource.ValueJson)
			out <- AppliedPersonas{Collection: collection, Version: response.Resource.Version, Err: err}
		}
	}()
	return out, nil
}

func (c *Client) Schedules(ctx context.Context) ([]cronstore.JobSpec, int64, error) {
	response, err := c.rpc.Query(ctx, &pb.QueryRequest{Kind: SchedulesKind})
	if err != nil {
		return nil, 0, clientError(err)
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
		return 0, clientError(err)
	}
	return response.Resource.Version, nil
}

func clientError(err error) error {
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
