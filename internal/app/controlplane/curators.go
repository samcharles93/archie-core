package controlplane

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/curator"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
)

const CuratorsKind = "curators"

func curatorDefinition() Definition {
	return Definition{
		Kind: CuratorsKind, Title: "Curators", Document: []config.CuratorDefinition{}, ApplyMode: "live",
		Seed: func(cfg config.Config) any { return cfg.Curators },
		Validate: func(input []byte) error {
			return validateAs(input, func(defs []config.CuratorDefinition) error {
				names := map[string]bool{"skill": true, "session-memory": true}
				for _, def := range defs {
					if strings.TrimSpace(def.Name) == "" || names[def.Name] {
						return fmt.Errorf("curator name %q is empty, reserved or repeated", def.Name)
					}
					names[def.Name] = true
					if def.Interval <= 0 {
						return fmt.Errorf("curator %s: interval must be positive", def.Name)
					}
					if err := CuratorDefinition(def).Manifest.Validate(); err != nil {
						return fmt.Errorf("curator %s: %w", def.Name, err)
					}
				}
				return nil
			})
		},
	}
}

func CuratorDefinition(def config.CuratorDefinition) curator.Definition {
	return curator.Definition{Name: def.Name, Enabled: def.Enabled, Instructions: def.Instructions, Manifest: curator.Manifest{
		Interval: def.Interval.Std(), Cooldown: def.Cooldown.Std(), OnInput: def.OnInput, Tools: def.Tools, Skills: def.Skills, MemoryEngine: def.MemoryEngine, Conversations: def.Conversations, Model: def.Model,
	}}
}

func (c *Client) Curators(ctx context.Context) ([]config.CuratorDefinition, int64, error) {
	var defs []config.CuratorDefinition
	version, _, err := c.Query(ctx, CuratorsKind, func(value []byte) error { return json.Unmarshal(value, &defs) })
	return defs, version, err
}

type AppliedCurators struct {
	Definitions []config.CuratorDefinition
	Version     int64
	Err         error
}

func (c *Client) WatchCurators(ctx context.Context, after int64) (<-chan AppliedCurators, error) {
	stream, err := c.rpc.Watch(ctx, &pb.WatchRequest{Kind: CuratorsKind, AfterVersion: after})
	if err != nil {
		return nil, controlplanerpc.ClientError(err)
	}
	return watchUpdates(ctx, stream, func(resource *pb.Resource) AppliedCurators {
		update := AppliedCurators{Version: resource.Version}
		update.Err = json.Unmarshal(resource.ValueJson, &update.Definitions)
		return update
	}, func(err error) AppliedCurators { return AppliedCurators{Err: err} }), nil
}
