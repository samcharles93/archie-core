package secretengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"

	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
	"github.com/samcharles93/archie-core/internal/infrastructure/controlplanerpc"
	"github.com/samcharles93/archie-core/internal/infrastructure/extension"
	"github.com/samcharles93/archie-core/internal/secret"
)

// Source is what the runner reads.
type Source = extension.Source

// Runner keeps the enabled secret-engine extensions running and registered.
// An extension runs when its package is installed, its authority is accepted
// and the extension-settings resource enables it; the process sees only the
// environment variables the accepted authority names.
type Runner struct {
	host     *extension.Host
	registry *secret.Registry
	source   Source
	cacheDir string
	log      *slog.Logger

	// report records the extension-settings version a sync applied, with its
	// outcome. Nil reports nothing.
	report func(ctx context.Context, version int64, err error)

	// changed runs after a sync starts or stops an engine, so a service can
	// resolve again the references that engine answers. Nil does nothing.
	changed func()

	mu      sync.Mutex
	running map[string]runningEngine
}

// OnChange makes every sync that starts or stops an engine call changed.
func (r *Runner) OnChange(changed func()) {
	r.mu.Lock()
	r.changed = changed
	r.mu.Unlock()
}

type runningEngine struct {
	digest   string
	settings string
}

// NewRunner returns a Runner that registers engines into registry and keeps
// extracted binaries under cacheDir.
func NewRunner(host *extension.Host, registry *secret.Registry, source Source, cacheDir string, log *slog.Logger) *Runner {
	return &Runner{host: host, registry: registry, source: source, cacheDir: cacheDir, log: log, running: make(map[string]runningEngine)}
}

// ReportTo makes every sync record its outcome through report.
func (r *Runner) ReportTo(report func(ctx context.Context, version int64, err error)) {
	r.report = report
}

// Run syncs every interval until ctx ends.
func (r *Runner) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Sync(ctx); err != nil {
				r.log.Warn("secret engine sync", "err", err)
			}
		}
	}
}

// Sync starts the engines that should run and are not, restarts those whose
// package, settings or process changed, and stops the rest. It returns the
// problems it met; a failed engine does not stop the others.
func (r *Runner) Sync(ctx context.Context) error {
	settings, version, err := r.source.Enabled(ctx)
	if err == nil {
		err = r.apply(ctx, settings)
	}
	if r.report != nil && version > 0 {
		r.report(ctx, version, err)
	}
	return err
}

func (r *Runner) apply(ctx context.Context, settings map[string]controlplanerpc.ExtensionSetting) error {
	installed, err := r.source.Packages.ListInstalled(ctx)
	if err != nil {
		return fmt.Errorf("list installed packages: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	before := r.runningDigests()
	defer func() {
		if r.changed != nil && !maps.Equal(before, r.runningDigests()) {
			go r.changed()
		}
	}()

	var problems []error
	want := make(map[string]bool)
	for _, pkg := range installed {
		setting, ok := settings[pkg.Name]
		if !ok || !setting.Enabled || pkg.AcceptedAuthority == nil || len(secretEngines(pkg.Descriptor)) == 0 {
			continue
		}
		want[pkg.Name] = true
		if err := r.ensure(ctx, pkg, setting); err != nil {
			problems = append(problems, fmt.Errorf("secret engine %q: %w", pkg.Name, err))
		}
	}
	for name := range r.running {
		if want[name] {
			continue
		}
		r.host.Stop(name)
		r.registry.Unregister(name)
		delete(r.running, name)
		_ = os.RemoveAll(filepath.Join(r.cacheDir, name))
	}
	return errors.Join(problems...)
}

func (r *Runner) runningDigests() map[string]string {
	out := make(map[string]string, len(r.running))
	for name, engine := range r.running {
		out[name] = engine.digest
	}
	return out
}

func secretEngines(d storepkg.Descriptor) []storepkg.Extension {
	var out []storepkg.Extension
	for _, extension := range d.Contributes.Extensions {
		if extension.Surface == storepkg.SurfaceSecretEngine {
			out = append(out, extension)
		}
	}
	return out
}

func (r *Runner) ensure(ctx context.Context, pkg storepkg.Installed, setting controlplanerpc.ExtensionSetting) error {
	encoded, err := json.Marshal(setting.Settings)
	if err != nil {
		return err
	}
	have, running := r.running[pkg.Name]
	current := running && have.digest == pkg.Digest && have.settings == string(encoded) && r.host.Alive(pkg.Name)
	if current {
		return nil
	}
	engines := secretEngines(pkg.Descriptor)
	if len(engines) != 1 {
		return fmt.Errorf("a package serves one secret engine, this one declares %d", len(engines))
	}
	binary, sum, err := extension.Materialize(ctx, r.source.Packages, r.cacheDir, pkg, engines[0].Path)
	if err != nil {
		return err
	}
	spec := extension.Spec{Name: pkg.Name, Path: binary, SHA256: sum, Env: slices.Clone(pkg.AcceptedAuthority.Env), Egress: slices.Clone(pkg.AcceptedAuthority.EgressHosts)}
	engine, err := Start(ctx, r.host, spec, setting.Settings)
	if err != nil {
		return err
	}
	r.registry.Register(engine)
	r.running[pkg.Name] = runningEngine{digest: pkg.Digest, settings: string(encoded)}
	return nil
}

// Close stops every running engine.
func (r *Runner) Close() { r.host.Close() }

// Supervise starts a Runner for process, runs its first sync before returning
// so the caller's next resolution can see the engines, and keeps syncing until
// ctx ends. A failed sync degrades references naming an extension engine and
// is retried each tick; it never stops the caller. The returned Runner's Close
// stops the engines.
func Supervise(ctx context.Context, registry *secret.Registry, source Source, process string, report func(ctx context.Context, version int64, err error), log *slog.Logger) *Runner {
	cache, err := os.UserCacheDir()
	if err != nil {
		log.Warn("secret engines unavailable: no cache directory", "err", err)
		return &Runner{host: extension.NewHost(hclog.NewNullLogger())}
	}
	host := extension.NewHost(hclog.New(&hclog.LoggerOptions{Name: "extension", Level: hclog.Warn}))
	runner := NewRunner(host, registry, source, filepath.Join(cache, "archie", "extensions", process), log)
	runner.ReportTo(report)
	if err := runner.Sync(ctx); err != nil {
		log.Warn("secret engines", "err", err)
	}
	go runner.Run(ctx, applystatus.RestampInterval)
	return runner
}
