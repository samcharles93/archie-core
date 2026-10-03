package servicekit

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/secret"
)

// RuntimeConfig layers the control plane's stored settings over base, lets
// adjust apply the caller's own live settings, then validates the result and
// resolves its provider secrets. The outcome is reported for every layered
// kind, so a refused document keeps the version still running on the record.
func RuntimeConfig(ctx context.Context, cp *controlplane.Client, status *applystatus.Reporter, secrets *secret.Registry, log *slog.Logger, base config.Config, adjust func(*config.Config)) (config.Config, map[string]int64, error) {
	cfg, versions, err := cp.RuntimeConfig(ctx, base)
	if err != nil {
		return config.Config{}, nil, err
	}
	if adjust != nil {
		adjust(&cfg)
	}
	report := func(applyErr error) {
		for kind, version := range versions {
			status.Report(ctx, kind, version, applyErr)
		}
	}
	if err := configuration.Validate(&cfg); err != nil {
		wrapped := fmt.Errorf("validate database settings: %w", err)
		report(wrapped)
		return config.Config{}, nil, wrapped
	}
	if err := ResolveProviderSecrets(&cfg, secrets, log); err != nil {
		report(err)
		return config.Config{}, nil, err
	}
	report(nil)
	return cfg, versions, nil
}
