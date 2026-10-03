package archied

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/eda/module"
)

// The directory families a plugin-settings document names and a
// reconciliation reads.
const reconcileModules = "module"

// pluginReconciler loads plugin and module files that appear or
// change in the directories the running configuration names, so an operator can
// drop a file beside a running daemon and have it take effect without a
// restart. Yaegi cannot unload an interpreter, so a removed file's code keeps
// running and the reconciler reports it through apply status until the process
// restarts.
//
// Files are content-hashed, so a tick loads only what appeared or changed: the
// boot load's files are seeded as already loaded. A file whose load fails is
// forgotten until its content changes, the same degrade-and-skip rule the boot
// loaders apply.
type pluginReconciler struct {
	log *slog.Logger
	pluginReconcileTargets

	// reconcileMu serializes reconciliations: the poller's tick and a stored
	// plugin-settings update both run one, and they share the loaded set.
	reconcileMu sync.Mutex
	loaded      map[string]loadedPluginFile
}

type pluginReconcileTargets struct {
	modules *module.ModuleRegistry
	// dirs returns the running configuration, the source of the
	// directories. It is read each tick so a changed directory retargets the
	// next reconciliation without a restart.
	dirs func() config.Config
	// relayer re-runs the configuration layering, which re-resolves provider
	// credentials against the engines running by then. Nil in tests that do not
	// layer.
	relayer func(ctx context.Context) error
	// report publishes the reconciliation outcome, including a nil error on a
	// clean pass so an outstanding removal clears.
	report func(ctx context.Context, err error)
}

type loadedPluginFile struct {
	hash     string
	category string
	// failed marks a file whose last load failed. It is not reported as a
	// removal while it is still present, and a content change retries it.
	failed bool
}

func newPluginReconciler(log *slog.Logger, targets pluginReconcileTargets) *pluginReconciler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &pluginReconciler{
		log:                    log,
		pluginReconcileTargets: targets,
		loaded:                 make(map[string]loadedPluginFile),
	}
}

// seed records the files the boot load already evaluated, so the first tick
// loads only what appeared or changed since boot. The daemon calls it after
// loadPlugins and loadWorkflows have run.
func (r *pluginReconciler) seed() {
	next := make(map[string]loadedPluginFile)
	for _, spec := range r.categories(r.dirs()) {
		paths, err := goFiles(spec.dir)
		if err != nil {
			r.log.Warn("plugin directory not seeded", "category", spec.category, "dir", spec.dir, "err", err)
			continue
		}
		for _, path := range paths {
			hash, err := fileHash(path)
			if err != nil {
				continue
			}
			next[path] = loadedPluginFile{hash: hash, category: spec.category}
		}
	}
	r.reconcileMu.Lock()
	r.loaded = next
	r.reconcileMu.Unlock()
}

// run reconciles on interval until ctx ends. The interval is the apply-status
// restamp constant, so a dropped file needs no filesystem watch and a
// reconciliation report is refreshed at the same cadence as every other apply
// status record.
func (r *pluginReconciler) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.reconcile(ctx)
		}
	}
}

type reconcileCategory struct {
	category string
	dir      string
}

func (r *pluginReconciler) categories(cfg config.Config) []reconcileCategory {
	return []reconcileCategory{
		{reconcileModules, cfg.ModuleDir},
	}
}

// reconcile re-reads the plugin and module directories against the running config and
// loads every new or changed file. It returns the outstanding problems -- a
// removal, a failed load, an unreadable directory -- joined, and reports the
// same through apply status. A nil return is a clean pass and clears the
// record's error.
func (r *pluginReconciler) reconcile(ctx context.Context) error {
	r.reconcileMu.Lock()
	defer r.reconcileMu.Unlock()

	var problems []error
	next := make(map[string]loadedPluginFile, len(r.loaded))

	for _, spec := range r.categories(r.dirs()) {
		problems = append(problems, r.reconcileCategory(ctx, spec, next)...)
	}

	for path, file := range r.loaded {
		if _, ok := next[path]; ok || file.failed {
			continue
		}
		problems = append(problems, fmt.Errorf("%s file %s was removed; it keeps running until the process restarts", file.category, path))
	}

	r.loaded = next

	// The layering re-runs every tick: it re-resolves provider credentials, so a
	// provider disabled at boot for an unresolvable engine is re-resolved on the
	// first tick that finds it
	if r.relayer != nil {
		if err := r.relayer(ctx); err != nil {
			problems = append(problems, fmt.Errorf("re-resolve provider credentials: %w", err))
		}
	}

	err := errors.Join(problems...)
	if r.report != nil {
		r.report(ctx, err)
	}
	return err
}

// reconcileCategory loads the new and changed files of one directory into
// next, returning the problems it met. A directory that became unreadable
// keeps the entries this category already had: it is not a removal of the
// files loaded before that.
func (r *pluginReconciler) reconcileCategory(ctx context.Context, spec reconcileCategory, next map[string]loadedPluginFile) []error {
	var problems []error
	paths, err := goFiles(spec.dir)
	if err != nil {
		problems = append(problems, fmt.Errorf("%s directory %q: %w", spec.category, spec.dir, err))
		for path, file := range r.loaded {
			if file.category == spec.category {
				next[path] = file
			}
		}
		return problems
	}
	for _, path := range paths {
		hash, err := fileHash(path)
		if err != nil {
			problems = append(problems, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		if previous, ok := r.loaded[path]; ok && previous.hash == hash {
			next[path] = previous
			continue
		}
		if err := r.load(ctx, spec.category, path); err != nil {
			problems = append(problems, fmt.Errorf("%s %s: %w", spec.category, path, err))
			// Record the failed content so the same broken file is not retried
			// every tick; changing it retries the load.
			next[path] = loadedPluginFile{hash: hash, category: spec.category, failed: true}
			continue
		}
		next[path] = loadedPluginFile{hash: hash, category: spec.category}
		r.log.Info("runtime directory file loaded", "category", spec.category, "path", path)
	}
	return problems
}

func (r *pluginReconciler) load(ctx context.Context, category, path string) error {
	switch category {
	case reconcileModules:
		return r.loadModule(path)
	default:
		return fmt.Errorf("unknown reconcile category %q", category)
	}
}

func (r *pluginReconciler) loadModule(path string) error {
	if r.modules == nil {
		return errors.New("no module registry to load into")
	}
	kind := strings.TrimSuffix(filepath.Base(path), ".go")
	if !slices.Contains(module.Kinds(), kind) {
		// A .go file for a kind this build has no contract for; the boot load
		// iterates the known kinds and ignores it too.
		return nil
	}
	return r.modules.Register(kind, filepath.Dir(path))
}

// goFiles lists the .go files directly under dir, sorted. A missing directory
// reconciles to an empty set, the convention every boot loader already follows.
func goFiles(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	slices.Sort(paths)
	return paths, nil
}

func fileHash(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:]), nil
}
