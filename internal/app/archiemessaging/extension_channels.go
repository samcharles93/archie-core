package archiemessaging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/go-hclog"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/channelext"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/secret"
)

// refSuffix marks a setting that holds an "engine:key" secret reference.
const refSuffix = "_ref"

// openExtensionChannels builds a channel for each installed channel package
// whose authority is accepted and that extension-settings enables. Changing
// the set takes a restart, as every channel binding does. Running extensions
// stop with ctx.
func openExtensionChannels(ctx context.Context, source extension.Source, secrets *secret.Registry, log *slog.Logger) []*channelInstance {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Warn("channel extensions unavailable: no cache directory", "err", err)
		return nil
	}
	settings, _, err := source.Enabled(ctx)
	if err != nil {
		log.Warn("channel extensions unavailable: extension settings", "err", err)
		return nil
	}
	installed, err := source.Packages.ListInstalled(ctx)
	if err != nil {
		log.Warn("channel extensions unavailable: installed packages", "err", err)
		return nil
	}
	host := extension.NewHost(hclog.New(&hclog.LoggerOptions{Name: "extension", Level: hclog.Warn}))
	var instances []*channelInstance
	for _, pkg := range installed {
		setting, ok := settings[pkg.Name]
		if !ok || !setting.Enabled || pkg.AcceptedAuthority == nil {
			continue
		}
		index := slices.IndexFunc(pkg.Descriptor.Contributes.Extensions, func(x storepkg.Extension) bool { return x.Surface == storepkg.SurfaceChannel })
		if index < 0 {
			continue
		}
		ch, err := buildExtensionChannel(ctx, host, source, filepath.Join(cache, "archie", "extensions", "channels"), pkg, pkg.Descriptor.Contributes.Extensions[index].Path, setting.Settings, secrets)
		if err != nil {
			log.Warn("channel extension not started", "channel", pkg.Name, "err", err)
			continue
		}
		instances = append(instances, &channelInstance{
			name:    pkg.Name,
			channel: ch,
			rebuild: func(ResolvedConfig) (channels.Channel, error) { return ch, nil },
		})
	}
	go func() {
		<-ctx.Done()
		host.Close()
	}()
	return instances
}

func buildExtensionChannel(ctx context.Context, host *extension.Host, source extension.Source, cacheDir string, pkg storepkg.Installed, file string, settings map[string]string, secrets *secret.Registry) (channels.Channel, error) {
	binary, sum, err := extension.Materialize(ctx, source.Packages, cacheDir, pkg, file)
	if err != nil {
		return nil, err
	}
	plain := make(map[string]string, len(settings))
	resolved := make(map[string]string)
	for key, value := range settings {
		name, isRef := strings.CutSuffix(key, refSuffix)
		if !isRef {
			plain[key] = value
			continue
		}
		engine, ref, ok := strings.Cut(value, ":")
		if !ok {
			return nil, fmt.Errorf("setting %q: want engine:key", key)
		}
		secretValue, err := secrets.Resolve(secret.SecretRef{Engine: engine, Key: ref})
		if err != nil {
			return nil, fmt.Errorf("setting %q: %w", key, err)
		}
		resolved[name] = secretValue
	}
	spec := extension.Spec{Name: "channel/" + pkg.Name, Path: binary, SHA256: sum, Env: slices.Clone(pkg.AcceptedAuthority.Env)}
	return channelext.New(host, pkg.Name, spec, plain, resolved), nil
}
