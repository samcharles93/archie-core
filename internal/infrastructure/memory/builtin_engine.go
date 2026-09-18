// Package memory implements domain/memory.MemoryEngine over concrete
// backends. The builtin engine persists each scope's records as markdown
// under its root -- human-readable and hand-editable -- with a per-scope
// HISTORY.md retaining the states that are no longer live. See
// docs/prds/memory-engine-unification.md for the decision this implements.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/infrastructure/memory/builtin"
)

// EngineName is the name BuiltinEngine registers under and the default the
// config surface resolves to when no engine is configured.
const EngineName = "builtin"

// defaultSectionName is the section a record with an empty Kind is written
// to.
const defaultSectionName = "general"

// liveFileName is the one live markdown document per scope, holding that
// scope's heads.
const liveFileName = "OBSERVATIONS.md"

// defaultQueryLimit bounds Query when the caller names no limit: 0 in
// Query.Limit means "the engine's default", never unlimited, and List is the
// unbounded read for one scope.
const defaultQueryLimit = 100

// contentScanner is the family's content scanner, applied in Create and
// Update. Those two are the one choke point every producer crosses, so the
// curator's model-extracted content is covered as well as the chat tool's
// (docs/prds/memory-engine-unification.md §7).
var contentScanner domainmemory.Scanner = &domainmemory.DefaultScanner{}

// scopeStores is one scope's on-disk pair: the heads, and the states that
// used to be heads.
type scopeStores struct {
	live    *builtin.Store
	history *builtin.Store
}

// BuiltinEngine implements domain/memory.MemoryEngine over one pair of
// builtin.Store documents per scope.
type BuiltinEngine struct {
	root         string
	maxFileBytes int

	mu     sync.Mutex
	stores map[string]*scopeStores // keyed by scope key, lazily created

	clock domainmemory.Clock
}

// NewBuiltinEngine builds an engine that persists each scope under its own
// subdirectory of root, named hex(sha256(scope.Key())): never the key or an
// id as a path component, so an opaque channel id can never traverse out of
// root and two ids a filesystem treats as equivalent can never collide.
// maxFileBytes <= 0 uses the store's own default for the live document;
// HISTORY.md always uses historyMaxFileBytes.
func NewBuiltinEngine(root string, maxFileBytes int) *BuiltinEngine {
	return &BuiltinEngine{root: root, maxFileBytes: maxFileBytes, stores: make(map[string]*scopeStores)}
}

func (e *BuiltinEngine) Name() string    { return EngineName }
func (e *BuiltinEngine) Version() string { return "1" }

func (e *BuiltinEngine) Manifest() domainmemory.Manifest {
	return domainmemory.Manifest{RequiresNetwork: false}
}

// Bind takes the registrar's clock, which stamps CreatedAt/UpdatedAt.
//
// It deliberately ignores Registrar.Events. The only warn-level finding this
// engine produces is a scanner hit, and there is no ratified memory event
// vocabulary to emit it in: internal/events declares kinds for tasks,
// curators, workflow stages and scheduling, none for memory, and the
// composition binds no memory event sink at all (bootstrap registers the
// engine with an empty Registrar). Emitting an invented kind into a sink
// nobody binds would look wired and not be.
func (e *BuiltinEngine) Bind(host domainmemory.Registrar) { e.clock = host.Clock }

func (e *BuiltinEngine) Start(context.Context) error { return nil }

func (e *BuiltinEngine) Health(context.Context) domainmemory.Health {
	return domainmemory.Health{Status: domainmemory.HealthHealthy}
}

func (e *BuiltinEngine) Stop(context.Context) error { return nil }

// systemClock is the fallback for an engine no registry bound. Registrar's
// own doc says a nil Clock is replaced at construction, but a directly
// constructed engine (a test, an embedder) has no registry at all, and a
// clock nil-panic on a write would be a poor way to find that out.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var fallbackClock domainmemory.Clock = systemClock{}

func (e *BuiltinEngine) now() time.Time {
	if e.clock == nil {
		return fallbackClock.Now()
	}
	return e.clock.Now()
}

// ── storage ────────────────────────────────────────────────────────────

// scopeStoresFor returns the scope's document pair, opening it on first use
// and caching it. create is false on every read path: a read must not leave
// a scope directory behind, and a scope nothing was ever written to answers
// as absent rather than as empty-but-created.
//
// Callers must have validated the scope already: Scope.Key() is empty for an
// invalid scope, and caching on "" would pool every invalid scope into one
// store under one directory.
func (e *BuiltinEngine) scopeStoresFor(scope domainmemory.Scope, create bool) (*scopeStores, bool, error) {
	key := scope.Key()
	e.mu.Lock()
	defer e.mu.Unlock()
	if s, ok := e.stores[key]; ok {
		return s, true, nil
	}

	dir := filepath.Join(e.root, scopeDirName(scope))
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false, fmt.Errorf("memory: builtin engine: scope directory %s unusable: %w", dir, err)
		}
	} else if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("memory: builtin engine: scope directory %s unusable: %w", dir, err)
	}

	live, err := builtin.NewStore(filepath.Join(dir, liveFileName), e.maxFileBytes)
	if err != nil {
		return nil, false, err
	}
	history, err := builtin.NewStore(filepath.Join(dir, historyFileName), historyMaxFileBytes)
	if err != nil {
		return nil, false, err
	}
	stores := &scopeStores{live: live, history: history}
	e.stores[key] = stores
	return stores, true, nil
}

// scopeDirName maps a scope to a filesystem-safe, collision-resistant
// directory name.
func scopeDirName(scope domainmemory.Scope) string {
	sum := sha256.Sum256([]byte(scope.Key()))
	return hex.EncodeToString(sum[:])
}

// sectionFor returns the section a record's Kind maps to, validated the same
// way builtin validates any section name -- so an invalid Kind fails with the
// error shape the memory tool already returns for an invalid section, not a
// new one.
func sectionFor(kind string) (string, error) {
	if kind == "" {
		kind = defaultSectionName
	}
	if err := builtin.ValidateSectionName(kind); err != nil {
		return "", err
	}
	return kind, nil
}

// scanContent applies the family's scanner to content about to be persisted.
// A block-level threat fails the write loudly -- loudly, because a scanner
// hit that only logs is a control that looks wired and is not -- while a
// warn-level hit is allowed through (see Bind for why it is not also
// emitted).
func scanContent(content string) error {
	result := contentScanner.ScanContent(content)
	if result.Level != domainmemory.ThreatBlock {
		return nil
	}
	return fmt.Errorf("memory: builtin engine: refusing to persist content: %s", result.Message)
}

// storeContent returns the content a block will actually hold, so that the
// record Create and Update answer with is the record a later Get reads back
// rather than what the caller handed in.
//
// The trimming is not cosmetic: builtin.Store trims a block, and a block ends
// at a blank line, so content that begins with whitespace would push the
// block's own boundary and come back truncated -- or, for whitespace that is
// itself a blank line, as an empty record. Content that is nothing but
// whitespace is rejected here for the same reason.
func storeContent(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", errors.New("memory: builtin engine: content must not be empty")
	}
	return trimmed, nil
}

// findBlock returns the live block addressed by id.
func findBlock(store *builtin.Store, id domainmemory.RecordID) (parsedBlock, bool) {
	for _, block := range parseBlocks(store.Render()) {
		if block.marker.ID == string(id) {
			return block, true
		}
	}
	return parsedBlock{}, false
}

func notFound(scope domainmemory.Scope, id domainmemory.RecordID) error {
	return fmt.Errorf("memory: builtin engine: %w: id %q in scope %s", domainmemory.ErrNotFound, id, scope)
}

// ── Store ──────────────────────────────────────────────────────────────

// Create records one new memory: the id is assigned once, here, and is the
// only address the record ever has.
func (e *BuiltinEngine) Create(_ context.Context, in domainmemory.NewRecord) (domainmemory.Record, error) {
	if err := in.Validate(); err != nil {
		return domainmemory.Record{}, err
	}
	section, err := sectionFor(in.Kind)
	if err != nil {
		return domainmemory.Record{}, err
	}
	if err := scanContent(in.Content); err != nil {
		return domainmemory.Record{}, err
	}
	content, err := storeContent(in.Content)
	if err != nil {
		return domainmemory.Record{}, err
	}
	stores, _, err := e.scopeStoresFor(in.Scope, true)
	if err != nil {
		return domainmemory.Record{}, err
	}

	now := e.now()
	marker := markerData{
		ID:         uuid.NewString(),
		Revision:   1,
		Kind:       section,
		Author:     in.Author,
		OriginUser: string(in.OriginUser),
		Source:     in.Source,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	block, err := renderBlock(marker, content)
	if err != nil {
		return domainmemory.Record{}, err
	}
	if _, err := stores.live.Add(section, block); err != nil {
		return domainmemory.Record{}, err
	}
	return recordOf(marker, in.Scope, content), nil
}

func (e *BuiltinEngine) Get(_ context.Context, scope domainmemory.Scope, id domainmemory.RecordID) (domainmemory.Record, error) {
	if err := scope.Validate(); err != nil {
		return domainmemory.Record{}, err
	}
	if id == "" {
		return domainmemory.Record{}, errors.New("memory: builtin engine: id must not be empty")
	}
	stores, ok, err := e.scopeStoresFor(scope, false)
	if err != nil {
		return domainmemory.Record{}, err
	}
	if !ok {
		return domainmemory.Record{}, notFound(scope, id)
	}
	block, ok := findBlock(stores.live, id)
	if !ok {
		return domainmemory.Record{}, notFound(scope, id)
	}
	return block.record(scope), nil
}

// List returns one scope's heads, most recently updated first.
func (e *BuiltinEngine) List(_ context.Context, scope domainmemory.Scope) ([]domainmemory.Record, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	stores, ok, err := e.scopeStoresFor(scope, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	blocks := parseBlocks(stores.live.Render())
	records := make([]domainmemory.Record, 0, len(blocks))
	for _, block := range blocks {
		records = append(records, block.record(scope))
	}
	return orderHeads(records), nil
}

// orderHeads returns heads most recently updated first. The input is in
// document order, which is creation order for a document this engine wrote;
// reversing before the stable sort makes creation order the tie-break for two
// records sharing an UpdatedAt, which is the common case for a coarse clock or
// one Create burst.
func orderHeads(records []domainmemory.Record) []domainmemory.Record {
	slices.Reverse(records)
	slices.SortStableFunc(records, func(a, b domainmemory.Record) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return records
}

// Query returns the heads of exactly the scopes q names -- the caller's read
// set, never widened here -- grouped in the order those scopes were given and
// bounded by q.Limit across the whole result.
func (e *BuiltinEngine) Query(ctx context.Context, q domainmemory.Query) ([]domainmemory.Record, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = defaultQueryLimit
	}
	var out []domainmemory.Record
	for _, scope := range q.Scopes {
		heads, err := e.List(ctx, scope)
		if err != nil {
			return nil, err
		}
		for _, record := range heads {
			if q.Text != "" && !strings.Contains(record.Content, q.Text) {
				continue
			}
			out = append(out, record)
			if len(out) == limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Update supersedes a record's content in place and returns the new state.
//
// A record may not move between scopes, which is why RecordUpdate carries no
// scope to change: it is addressed by the scope it is already in.
func (e *BuiltinEngine) Update(_ context.Context, in domainmemory.RecordUpdate) (domainmemory.Record, error) {
	if err := in.Validate(); err != nil {
		return domainmemory.Record{}, err
	}
	// Scanned before anything is written: a refused update must leave both
	// the record and its history untouched.
	if err := scanContent(in.Content); err != nil {
		return domainmemory.Record{}, err
	}
	content, err := storeContent(in.Content)
	if err != nil {
		return domainmemory.Record{}, err
	}
	stores, ok, err := e.scopeStoresFor(in.Scope, false)
	if err != nil {
		return domainmemory.Record{}, err
	}
	if !ok {
		return domainmemory.Record{}, notFound(in.Scope, in.ID)
	}
	current, ok := findBlock(stores.live, in.ID)
	if !ok {
		return domainmemory.Record{}, notFound(in.Scope, in.ID)
	}
	// Expected is what makes a lost update between two processes holding one
	// scope's document detectable: this engine keeps the document in memory
	// and rewrites it whole, without reloading or locking it (the PRD leaves
	// "two processes, one scope file" undecided). For this one record, the
	// revision the caller last saw is the check.
	if in.Expected != 0 && in.Expected != current.marker.Revision {
		return domainmemory.Record{}, fmt.Errorf(
			"memory: builtin engine: %w: record %q is at revision %d, not %d",
			domainmemory.ErrStaleRevision, in.ID, current.marker.Revision, in.Expected)
	}

	now := e.now()
	next := current.marker
	next.Revision = current.marker.Revision + 1
	next.Author = in.Author
	next.Source = in.Source
	next.UpdatedAt = now

	// Crash safety, in this order: the superseded state is durable in
	// HISTORY.md before the live block is touched. A crash between the two
	// leaves an extra history entry and an unchanged record -- recoverable,
	// per §2 -- and never a record whose previous state is gone.
	if err := appendHistory(stores.history, current.marker, current.content(), now, false); err != nil {
		return domainmemory.Record{}, err
	}
	block, err := renderBlock(next, content)
	if err != nil {
		return domainmemory.Record{}, err
	}
	if _, err := stores.live.Replace(current.section, current.text, block, true); err != nil {
		return domainmemory.Record{}, err
	}
	return recordOf(next, in.Scope, content), nil
}

// Forget removes a record's live state, retaining a deleted revision so its
// provenance outlives its content. Forgetting an id that is not there is not
// an error: the end state, id absent, already holds.
func (e *BuiltinEngine) Forget(_ context.Context, scope domainmemory.Scope, id domainmemory.RecordID) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if id == "" {
		return errors.New("memory: builtin engine: id must not be empty")
	}
	stores, ok, err := e.scopeStoresFor(scope, false)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	current, ok := findBlock(stores.live, id)
	if !ok {
		return nil
	}

	// The same crash-safe order as Update: the deleted revision is durable
	// before the live block disappears, so the record's provenance is never
	// the part that is lost.
	if err := appendHistory(stores.history, current.marker, current.content(), e.now(), true); err != nil {
		return err
	}
	_, err = stores.live.Remove(current.section, current.text, true)
	return err
}

// Revisions returns a record's retained states, oldest first, including the
// deleted revision Forget records. A record that never existed is empty, not
// an error.
func (e *BuiltinEngine) Revisions(_ context.Context, scope domainmemory.Scope, id domainmemory.RecordID) ([]domainmemory.Revision, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if id == "" {
		return nil, errors.New("memory: builtin engine: id must not be empty")
	}
	stores, ok, err := e.scopeStoresFor(scope, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return revisionsOf(stores.history, scope, id), nil
}
