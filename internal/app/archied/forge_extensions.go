package archied

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"

	"github.com/hashicorp/go-hclog"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/infrastructure/forgeext"
)

// forgeExtensions opens forge instances served by installed forge extensions:
// the package named by forge.type, with its authority accepted and enabled in
// extension-settings. Changing which extension serves a forge takes a restart,
// as every forge setting does.
type forgeExtensions struct {
	host     *extension.Host
	source   extension.Source
	cacheDir string
	log      *slog.Logger
}

func newForgeExtensions(source extension.Source, log *slog.Logger) *forgeExtensions {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Warn("forge extensions unavailable: no cache directory", "err", err)
		return nil
	}
	return &forgeExtensions{
		host:     extension.NewHost(hclog.New(&hclog.LoggerOptions{Name: "extension", Level: hclog.Warn})),
		source:   source,
		cacheDir: filepath.Join(cache, "archie", "extensions", "forge"),
		log:      log,
	}
}

// open returns the instance's forge when an enabled extension named typ serves
// it. handled is false when none does, so the caller falls back to the
// compiled-in forge; an extension that is enabled but will not start is
// handled, as a forge that fails rather than a different one.
func (e *forgeExtensions) open(ctx context.Context, typ, instance, host, token string) (f forge.Forge, handled bool) {
	if e == nil {
		return nil, false
	}
	settings, _, err := e.source.Enabled(ctx)
	if err != nil {
		e.log.Warn("forge extensions: extension settings unavailable", "err", err)
		return nil, false
	}
	setting, ok := settings[typ]
	if !ok || !setting.Enabled {
		return nil, false
	}
	installed, err := e.source.Packages.ListInstalled(ctx)
	if err != nil {
		e.log.Warn("forge extensions: installed packages unavailable", "err", err)
		return nil, false
	}
	for _, pkg := range installed {
		if pkg.Name != typ || pkg.AcceptedAuthority == nil {
			continue
		}
		index := slices.IndexFunc(pkg.Descriptor.Contributes.Extensions, func(x storepkg.Extension) bool { return x.Surface == storepkg.SurfaceForge })
		if index < 0 {
			continue
		}
		forgeClient, err := e.start(ctx, pkg, pkg.Descriptor.Contributes.Extensions[index].Path, instance, host, token, setting.Settings)
		if err != nil {
			e.log.Warn("forge disabled: extension did not start", "forge_type", typ, "instance", instance, "err", err)
			return forge.NewNoop(e.log), true
		}
		return forgeClient, true
	}
	return nil, false
}

func (e *forgeExtensions) start(ctx context.Context, pkg storepkg.Installed, file, instance, host, token string, settings map[string]string) (forge.Forge, error) {
	binary, sum, err := extension.Materialize(ctx, e.source.Packages, e.cacheDir, pkg, file)
	if err != nil {
		return nil, err
	}
	spec := extension.Spec{Name: "forge/" + instance, Path: binary, SHA256: sum, Env: slices.Clone(pkg.AcceptedAuthority.Env)}
	return forgeext.Open(ctx, e.host, forgeext.Instance{Spec: spec, Host: host, Token: token, Settings: settings}, e.log)
}

func (e *forgeExtensions) close() {
	if e != nil {
		e.host.Close()
	}
}
