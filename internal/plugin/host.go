package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/stableid"
)

// HostAPIVersion is the capability-host contract version understood by this
// archied binary.
const HostAPIVersion = "1.0.0"

var semanticVersion = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

const rollbackTimeout = 10 * time.Second

// CapabilityKind identifies one typed capability family contributed by a
// module, such as "tools", "channels", or "delivery".
type CapabilityKind string

// Permission identifies one host permission requested by a module.
type Permission string

// Manifest declares module identity, compatibility, dependencies, and the
// capability families it contributes. It contains metadata only.
type Manifest struct {
	ID           string
	Name         string
	Version      string
	APIVersion   string
	Capabilities []CapabilityKind
	Dependencies []string
	Permissions  []Permission
	ConfigSchema json.RawMessage
}

// Validate checks manifest identity, compatibility, declarations, and schema.
func (m Manifest) Validate() error {
	if !stableid.Valid(m.ID) {
		return fmt.Errorf("plugin manifest id %q is not a stable identifier", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return errors.New("plugin manifest name is required")
	}
	if _, err := parseSemver(m.Version); err != nil {
		return fmt.Errorf("plugin manifest version: %w", err)
	}
	apiVersion, err := parseSemver(m.APIVersion)
	if err != nil {
		return fmt.Errorf("plugin manifest api_version: %w", err)
	}
	hostVersion, err := parseSemver(HostAPIVersion)
	if err != nil {
		return fmt.Errorf("plugin host api version: %w", err)
	}
	if apiVersion.major != hostVersion.major || apiVersion.minor > hostVersion.minor {
		return fmt.Errorf(
			"plugin manifest api_version %q is incompatible with host API %q",
			m.APIVersion,
			HostAPIVersion,
		)
	}
	if err := validateIdentifiers("capability", capabilityStrings(m.Capabilities), true); err != nil {
		return err
	}
	if err := validateIdentifiers("dependency", m.Dependencies, false); err != nil {
		return err
	}
	if slices.Contains(m.Dependencies, m.ID) {
		return fmt.Errorf("plugin manifest %q depends on itself", m.ID)
	}
	if err := validateIdentifiers("permission", permissionStrings(m.Permissions), false); err != nil {
		return err
	}
	if len(m.ConfigSchema) > 0 {
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(m.ConfigSchema, &schema); err != nil {
			return fmt.Errorf("plugin manifest config schema: %w", err)
		}
		if schema == nil {
			return errors.New("plugin manifest config schema must be a JSON object")
		}
	}
	return nil
}

func (m Manifest) clone() Manifest {
	clone := m
	clone.Capabilities = append([]CapabilityKind(nil), m.Capabilities...)
	clone.Dependencies = append([]string(nil), m.Dependencies...)
	clone.Permissions = append([]Permission(nil), m.Permissions...)
	clone.ConfigSchema = append(json.RawMessage(nil), m.ConfigSchema...)
	return clone
}

type semver struct {
	major int
	minor int
}

func parseSemver(value string) (semver, error) {
	matches := semanticVersion.FindStringSubmatch(value)
	if matches == nil {
		return semver{}, fmt.Errorf("%q is not semantic version syntax", value)
	}
	if prerelease := matches[4]; prerelease != "" {
		for identifier := range strings.SplitSeq(prerelease, ".") {
			if len(identifier) > 1 && identifier[0] == '0' && isASCIIDigits(identifier) {
				return semver{}, fmt.Errorf(
					"%q has a numeric prerelease identifier with a leading zero",
					value,
				)
			}
		}
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return semver{}, fmt.Errorf("parse major version: %w", err)
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return semver{}, fmt.Errorf("parse minor version: %w", err)
	}
	return semver{major: major, minor: minor}, nil
}

func isASCIIDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func capabilityStrings(values []CapabilityKind) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func permissionStrings(values []Permission) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = string(value)
	}
	return out
}

func validateIdentifiers(kind string, values []string, required bool) error {
	if required && len(values) == 0 {
		return fmt.Errorf("plugin manifest requires at least one %s", kind)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !stableid.Valid(value) {
			return fmt.Errorf("plugin manifest %s %q is not a stable identifier", kind, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("plugin manifest has duplicate %s %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// Module is the capability host's metadata and lifecycle contract. Domain
// operations belong to typed capability-family interfaces, not here.
type Module interface {
	Manifest() Manifest
	Start(context.Context) error
	Stop(context.Context) error
}

// AdaptLegacy wraps a metadata-only Plugin as a no-op lifecycle module.
func AdaptLegacy(legacy Plugin) (Module, error) {
	if isNilPlugin(legacy) {
		return nil, errors.New("legacy plugin is nil")
	}
	name, version, err := safeLegacyInfo(legacy)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{
		ID:           name,
		Name:         name,
		Version:      version,
		APIVersion:   HostAPIVersion,
		Capabilities: []CapabilityKind{"plugin.metadata"},
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("legacy plugin manifest: %w", err)
	}
	return &legacyModule{manifest: manifest}, nil
}

type legacyModule struct {
	manifest Manifest
}

func (m *legacyModule) Manifest() Manifest {
	return m.manifest.clone()
}

func (m *legacyModule) Start(context.Context) error {
	return nil
}

func (m *legacyModule) Stop(context.Context) error {
	return nil
}

type hostState uint8

const (
	hostIdle hostState = iota
	hostStarting
	hostRunning
	hostStopping
	hostStopped
	hostFailed
)

type registeredModule struct {
	module   Module
	manifest Manifest
}

// Host validates module manifests and coordinates cross-family lifecycle.
// It deliberately exposes no service lookup or domain callback registration.
type Host struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	modules      map[string]*registeredModule
	registration []string
	started      []string
	state        hostState
}

// NewHost creates an empty capability host.
func NewHost() *Host {
	return &Host{modules: make(map[string]*registeredModule)}
}

// Register adds a module to the host. Registration is allowed only before the
// first Start call.
func (h *Host) Register(module Module) error {
	h.opMu.Lock()
	defer h.opMu.Unlock()

	manifest, err := prepareModule(module)
	if err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.ensureModulesLocked()
	if h.state != hostIdle {
		return errors.New("plugin host no longer accepts registrations")
	}
	if _, exists := h.modules[manifest.ID]; exists {
		return fmt.Errorf("plugin module %q is already registered", manifest.ID)
	}
	h.modules[manifest.ID] = &registeredModule{
		module:   module,
		manifest: manifest,
	}
	h.registration = append(h.registration, manifest.ID)
	return nil
}

// Start starts registered modules in dependency order. A failed start stops
// every module already started by this call in reverse order.
func (h *Host) Start(ctx context.Context) error {
	h.opMu.Lock()
	defer h.opMu.Unlock()

	h.mu.Lock()
	if h.state == hostRunning {
		h.mu.Unlock()
		return nil
	}
	if h.state != hostIdle {
		state := h.state
		h.mu.Unlock()
		return fmt.Errorf("plugin host cannot start from state %d", state)
	}
	order, err := h.startOrderLocked()
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.state = hostStarting
	h.mu.Unlock()

	started := make([]string, 0, len(order))
	for _, id := range order {
		module := h.module(id)
		if err := safeStart(ctx, id, module); err != nil {
			rollback := append(append([]string(nil), started...), id)
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
			failedStops, rollbackErr := h.stopIDs(rollbackCtx, reverseClone(rollback))
			cancel()
			h.mu.Lock()
			h.started = reverseClone(failedStops)
			h.state = hostFailed
			h.mu.Unlock()
			return errors.Join(err, rollbackErr)
		}
		started = append(started, id)
	}

	h.mu.Lock()
	h.started = append([]string(nil), started...)
	h.state = hostRunning
	h.mu.Unlock()
	return nil
}

// Stop stops started modules in reverse dependency order. It attempts every
// stop even when one module returns an error or panics.
func (h *Host) Stop(ctx context.Context) error {
	h.opMu.Lock()
	defer h.opMu.Unlock()

	h.mu.Lock()
	if len(h.started) == 0 {
		h.state = hostStopped
		h.mu.Unlock()
		return nil
	}
	h.state = hostStopping
	order := reverseClone(h.started)
	h.mu.Unlock()

	failedStops, err := h.stopIDs(ctx, order)
	h.mu.Lock()
	h.started = reverseClone(failedStops)
	if len(failedStops) == 0 {
		h.state = hostStopped
	} else {
		h.state = hostFailed
	}
	h.mu.Unlock()
	return err
}

func (h *Host) startOrderLocked() ([]string, error) {
	const (
		unvisited uint8 = iota
		visiting
		visited
	)
	visitState := make(map[string]uint8, len(h.modules))
	order := make([]string, 0, len(h.modules))
	var visit func(string) error
	visit = func(id string) error {
		switch visitState[id] {
		case visiting:
			return fmt.Errorf("plugin dependency cycle includes %q", id)
		case visited:
			return nil
		}
		entry, exists := h.modules[id]
		if !exists {
			return fmt.Errorf("plugin dependency %q is not registered", id)
		}
		visitState[id] = visiting
		for _, dependency := range entry.manifest.Dependencies {
			if err := visit(dependency); err != nil {
				return fmt.Errorf("plugin %q: %w", id, err)
			}
		}
		visitState[id] = visited
		order = append(order, id)
		return nil
	}
	for _, id := range h.registration {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func (h *Host) stopIDs(ctx context.Context, ids []string) ([]string, error) {
	var errs []error
	var failed []string
	for _, id := range ids {
		if err := safeStop(ctx, id, h.module(id)); err != nil {
			failed = append(failed, id)
			errs = append(errs, err)
			continue
		}
	}
	return failed, errors.Join(errs...)
}

func (h *Host) module(id string) Module {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.modules[id].module
}

func (h *Host) ensureModulesLocked() {
	if h.modules == nil {
		h.modules = make(map[string]*registeredModule)
	}
}

func isNilModule(module Module) bool {
	return isNilInterface(module)
}

func isNilPlugin(legacy Plugin) bool {
	return isNilInterface(legacy)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func safeLegacyInfo(legacy Plugin) (name, version string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("legacy plugin metadata panic: %v", recovered)
		}
	}()
	return legacy.Name(), legacy.Version(), nil
}

func safeManifest(module Module) (manifest Manifest, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("plugin manifest panic: %v", recovered)
		}
	}()
	return module.Manifest(), nil
}

func safeStart(ctx context.Context, id string, module Module) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("plugin module %q start panic: %v", id, recovered)
		}
	}()
	if err := module.Start(ctx); err != nil {
		return fmt.Errorf("start plugin module %q: %w", id, err)
	}
	return nil
}

func safeStop(ctx context.Context, id string, module Module) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("plugin module %q stop panic: %v", id, recovered)
		}
	}()
	if err := module.Stop(ctx); err != nil {
		return fmt.Errorf("stop plugin module %q: %w", id, err)
	}
	return nil
}

func reverseClone(values []string) []string {
	out := append([]string(nil), values...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

// Add registers and starts a module on a host that is already running, the
// running-state half of a live plugin load (docs/prds/plugin-settings-live.md).
// The module starts on its own -- every module registered before it is already
// running -- and a failed start removes it again without disturbing them. On a
// host that has not started, Add behaves like Register and the module starts
// with the rest.
func (h *Host) Add(ctx context.Context, module Module) error {
	h.opMu.Lock()
	defer h.opMu.Unlock()

	manifest, err := prepareModule(module)
	if err != nil {
		return err
	}

	h.mu.Lock()
	h.ensureModulesLocked()
	if _, exists := h.modules[manifest.ID]; exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin module %q is already registered", manifest.ID)
	}
	h.modules[manifest.ID] = &registeredModule{module: module, manifest: manifest}
	h.registration = append(h.registration, manifest.ID)
	state := h.state
	h.mu.Unlock()

	switch state {
	case hostIdle:
		// Not started yet: Start will pick this module up with the rest.
		return nil
	case hostRunning:
	default:
		h.remove(manifest.ID)
		return fmt.Errorf("plugin host cannot add a module from state %d", state)
	}

	// Every registered module is running while the host is running, so a
	// dependency that is not registered cannot be satisfied by a later module
	// in this pass; refuse and let the next reconciliation retry once it is.
	for _, dependency := range manifest.Dependencies {
		if !h.registered(dependency) {
			h.remove(manifest.ID)
			return fmt.Errorf("plugin module %q depends on unregistered module %q", manifest.ID, dependency)
		}
	}

	if err := safeStart(ctx, manifest.ID, module); err != nil {
		h.remove(manifest.ID)
		return err
	}
	h.mu.Lock()
	h.started = append(h.started, manifest.ID)
	h.mu.Unlock()
	return nil
}

// Replace swaps the module whose manifest id matches for a new one while the
// host is running. The old module is stopped and the new one started; when
// the new start fails the old one is restarted, so a refused replacement
// leaves the running set as it was. Replacing a module another running module
// depends on is refused rather than swapped, because the dependent would keep
// calling a stopped implementation.
func (h *Host) Replace(ctx context.Context, module Module) error {
	h.opMu.Lock()
	defer h.opMu.Unlock()

	manifest, err := prepareModule(module)
	if err != nil {
		return err
	}

	h.mu.Lock()
	previous, exists := h.modules[manifest.ID]
	if !exists {
		h.mu.Unlock()
		return fmt.Errorf("plugin module %q is not registered", manifest.ID)
	}
	if dependent, ok := h.runningDependentLocked(manifest.ID); ok {
		h.mu.Unlock()
		return fmt.Errorf("plugin module %q is required by running module %q and cannot be replaced", manifest.ID, dependent)
	}
	wasStarted := slices.Contains(h.started, manifest.ID)
	h.mu.Unlock()

	if !wasStarted {
		// Not running: swap the registration only, so a later Start uses the
		// replacement.
		h.mu.Lock()
		h.modules[manifest.ID] = &registeredModule{module: module, manifest: manifest}
		h.mu.Unlock()
		return nil
	}

	if err := safeStop(ctx, manifest.ID, previous.module); err != nil {
		return err
	}
	if err := safeStart(ctx, manifest.ID, module); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		restartErr := safeStart(rollbackCtx, manifest.ID, previous.module)
		cancel()
		return errors.Join(err, restartErr)
	}
	h.mu.Lock()
	h.modules[manifest.ID] = &registeredModule{module: module, manifest: manifest}
	h.mu.Unlock()
	return nil
}

// Has reports whether a module with id is registered, running or not.
func (h *Host) Has(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.modules[id]
	return ok
}

// Manifests returns the registered modules' manifests sorted by id. It is the
// read side of the running set: a live load reports what is loaded from here.
func (h *Host) Manifests() []Manifest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Manifest, 0, len(h.modules))
	for _, entry := range h.modules {
		out = append(out, entry.manifest.clone())
	}
	slices.SortFunc(out, func(a, b Manifest) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// prepareModule validates a module and returns its cloned manifest, the
// validation Register, Add and Replace all share.
func prepareModule(module Module) (Manifest, error) {
	if isNilModule(module) {
		return Manifest{}, errors.New("plugin module is nil")
	}
	manifest, err := safeManifest(module)
	if err != nil {
		return Manifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest.clone(), nil
}

// registered reports whether id is registered. The caller holds no lock.
func (h *Host) registered(id string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.modules[id]
	return ok
}

// runningDependentLocked returns a started module other than id that depends
// on id, which is what makes replacing id unsafe. The caller holds h.mu.
func (h *Host) runningDependentLocked(id string) (string, bool) {
	for _, startedID := range h.started {
		if startedID == id {
			continue
		}
		entry, ok := h.modules[startedID]
		if !ok {
			continue
		}
		if slices.Contains(entry.manifest.Dependencies, id) {
			return startedID, true
		}
	}
	return "", false
}

// remove unregisters id, the rollback of a refused Add.
func (h *Host) remove(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.modules, id)
	h.registration = slices.DeleteFunc(h.registration, func(v string) bool { return v == id })
}
