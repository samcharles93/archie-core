// Package indexing provides language-neutral workspace text indexing.
//
// Ported from tau: internal/indexing/codesearch.go @ 4893818d0c76 (2026-07-13).
// Mutations from the original:
//   - tau resolved its index paths from process-global config helpers
//     (config.WorkspaceIndexDir/WorkspaceIndexDBPath). archied passes config
//     explicitly, so paths arrive via [Config] instead.
//   - the helper subcommand is archied's rather than tau's
//     (cmd/archied/codesearch.go).
//   - deferred db.Close calls are explicitly discarded; archie-core runs
//     errcheck, tau does not.
//
// Refresh from upstream by diffing against that path and commit.
package indexing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"time"

	"github.com/google/codesearch/index"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const (
	// MaxCandidateFiles bounds helper output and grep argv construction.
	MaxCandidateFiles    = 2000
	maxCandidateArgBytes = 24 * 1024
	buildTimeout         = 10 * time.Minute
	metadataTimeout      = 5 * time.Second
)

// CandidateProvider returns files which may match a grep query. A false
// handled result tells the caller to use its normal unindexed search path.
type CandidateProvider interface {
	Candidates(context.Context, string, bool, bool) ([]string, bool)
}

// Manager owns the workspace index lifecycle. The codesearch implementation
// runs in a child Tau process so upstream fatal exits and mmap lifetime cannot
// terminate or leak inside the interactive agent process.
type Manager struct {
	root      string
	indexPath string
	dbPath    string
	runner    helperRunner
	log       *slog.Logger
}

// logger is nil-safe: a Manager may be constructed as a struct literal
// (the tests do), so defaulting at the point of use rather than in the
// constructor is what actually holds.
func (m *Manager) logger() *slog.Logger {
	if m == nil || m.log == nil {
		return slog.Default()
	}
	return m.log
}

type helperRunner interface {
	Build(context.Context, string, string) error
	Candidates(context.Context, string, string, bool, bool) ([]string, error)
}

type processRunner struct{}

// Config locates the index storage for a Manager.
//
// tau derived these from process-global helpers. archied resolves them in
// its own config layer ([indexing] in config.toml, defaulted off work_dir by
// config.finalize) and passes them down. Defaulting deliberately does NOT
// live here: this package stays free of archie-core's config so it remains
// a straight diff against its tau original.
type Config struct {
	// IndexDir holds the mmap-friendly codesearch sidecar files.
	IndexDir string
	// DBPath is the SQLite database holding index lifecycle metadata.
	DBPath string
	// Log receives index build failures. Without it a failed build is
	// invisible: the manager degrades to unindexed search and the reason
	// is only ever written to a database column nobody reads.
	Log *slog.Logger
}

// NewManager prepares lifecycle storage and starts an asynchronous atomic
// refresh. An existing index remains available while the refresh runs.
func NewManager(ctx context.Context, cfg Config, root string) (*Manager, error) {
	if strings.TrimSpace(cfg.IndexDir) == "" {
		return nil, fmt.Errorf("indexing: IndexDir is required")
	}
	if strings.TrimSpace(cfg.DBPath) == "" {
		return nil, fmt.Errorf("indexing: DBPath is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	if err := os.MkdirAll(cfg.IndexDir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace index directory: %w", err)
	}
	m := &Manager{
		root:      filepath.Clean(absRoot),
		indexPath: filepath.Join(cfg.IndexDir, workspaceKey(absRoot)+".csearch"),
		dbPath:    cfg.DBPath,
		runner:    processRunner{},
		log:       cfg.Log,
	}
	if err := m.ensureSchema(ctx); err != nil {
		return nil, err
	}
	m.refreshAsync(ctx)
	return m, nil
}

// Candidates implements the tools.GrepIndex contract.
func (m *Manager) Candidates(ctx context.Context, pattern string, literal, caseSensitive bool) ([]string, bool) {
	state, err := m.state(ctx)
	if err != nil || state.IndexedAt.IsZero() {
		return nil, false
	}
	if _, err := os.Stat(state.IndexPath); err != nil {
		return nil, false
	}
	files, err := m.runner.Candidates(ctx, state.IndexPath, pattern, literal, caseSensitive)
	if err != nil {
		return nil, false
	}

	// The sidecar is a snapshot. Include files changed or created since the
	// build began so indexed search cannot miss live workspace edits.
	workspaceFiles, err := WorkspaceFiles(ctx, m.root)
	if err != nil {
		return nil, false
	}
	changedFiles := changedGitFiles(ctx, m.root)
	seen := make(map[string]struct{}, len(files))
	merged := make([]string, 0, len(files))
	argumentBytes := 0
	add := func(path string) {
		path, ok := confinedWorkspaceFile(m.root, path)
		if !ok {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		merged = append(merged, path)
		argumentBytes += len(path) + 1
	}
	for _, path := range files {
		add(path)
	}
	for _, path := range workspaceFiles {
		info, statErr := os.Stat(path)
		_, changed := changedFiles[path]
		if statErr == nil && (changed || !info.ModTime().Before(state.IndexedAt)) {
			add(path)
		}
	}
	if len(merged) == 0 || len(merged) > MaxCandidateFiles || argumentBytes > maxCandidateArgBytes {
		return nil, false
	}
	slices.Sort(merged)
	return merged, true
}

func changedGitFiles(ctx context.Context, root string) map[string]struct{} {
	changed := map[string]struct{}{}
	commands := [][]string{
		{"-C", root, "ls-files", "-m", "-o", "--exclude-standard", "-z"},
		{"-C", root, "diff", "--cached", "--name-only", "--diff-filter=ACMR", "-z"},
	}
	for _, args := range commands {
		out, err := exec.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			return map[string]struct{}{}
		}
		for name := range strings.SplitSeq(string(out), "\x00") {
			if name != "" {
				path := filepath.Join(root, filepath.FromSlash(name))
				if confined, ok := confinedWorkspaceFile(root, path); ok {
					changed[confined] = struct{}{}
				}
			}
		}
	}
	return changed
}

type indexState struct {
	IndexPath string
	IndexedAt time.Time
}

func (m *Manager) refreshAsync(parent context.Context) {
	go func() {
		ctx, cancel := context.WithTimeout(parent, buildTimeout)
		defer cancel()
		startedAt := time.Now().UTC()
		buildID := uuid.NewString()
		previous, _ := m.state(ctx)
		claimed, err := m.claimBuild(ctx, startedAt, buildID)
		if err != nil {
			// Degrading to unindexed search is safe but slow, and until
			// this is logged the only record of why is a database column
			// with no reader.
			m.logger().Warn("workspace index build not claimed", "root", m.root, "error", err)
			return
		}
		if !claimed {
			// Another process holds the build claim; not an error.
			return
		}
		finalPath := m.indexPath + "." + buildID
		tmp := finalPath + ".tmp"
		_ = os.Remove(tmp)
		if err := m.runner.Build(ctx, m.root, tmp); err != nil {
			_ = os.Remove(tmp)
			m.logger().Warn("workspace index build failed; falling back to unindexed search",
				"root", m.root, "error", err)
			m.finishBuild(parent, buildID, "error", time.Time{}, "", err.Error())
			return
		}
		if err := os.Rename(tmp, finalPath); err != nil {
			_ = os.Remove(tmp)
			m.logger().Warn("workspace index could not be published",
				"root", m.root, "error", err)
			m.finishBuild(parent, buildID, "error", time.Time{}, "", err.Error())
			return
		}
		if !m.finishBuild(parent, buildID, "ready", startedAt, finalPath, "") {
			_ = os.Remove(finalPath)
			m.logger().Warn("workspace index build superseded before publication", "root", m.root)
			return
		}
		m.logger().Info("workspace index ready", "root", m.root, "index", finalPath)
		if previous.IndexPath != "" && previous.IndexPath != finalPath &&
			(previous.IndexPath == m.indexPath || strings.HasPrefix(previous.IndexPath, m.indexPath+".")) {
			_ = os.Remove(previous.IndexPath)
		}
	}()
}

func (m *Manager) finishBuild(parent context.Context, buildID, status string, indexedAt time.Time, indexPath, lastError string) bool {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), metadataTimeout)
	defer cancel()
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		return false
	}
	// Each helper opens its own single-connection handle and the write has
	// already been committed by the time this runs, so a Close error carries
	// no recoverable information.
	defer func() { _ = db.Close() }()
	indexed := ""
	if !indexedAt.IsZero() {
		indexed = indexedAt.Format(time.RFC3339Nano)
	}
	result, err := db.ExecContext(ctx, `UPDATE workspace_indexes SET
		index_path=CASE WHEN ? = 'ready' THEN ? ELSE index_path END,
		status=?, indexed_at=CASE WHEN ? = 'ready' THEN ? ELSE indexed_at END,
		last_error=?, updated_at=?, generation=generation + CASE WHEN ? = 'ready' THEN 1 ELSE 0 END
		WHERE root=? AND build_id=?`, status, indexPath, status, status, indexed,
		lastError, time.Now().UTC().Format(time.RFC3339Nano), status, m.root, buildID)
	if err != nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}

func (m *Manager) claimBuild(ctx context.Context, startedAt time.Time, buildID string) (bool, error) {
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		return false, err
	}
	// Each helper opens its own single-connection handle and the write has
	// already been committed by the time this runs, so a Close error carries
	// no recoverable information.
	defer func() { _ = db.Close() }()
	cutoff := startedAt.Add(-buildTimeout).Format(time.RFC3339Nano)
	result, err := db.ExecContext(ctx, `INSERT INTO workspace_indexes (root, index_path, status, indexed_at, build_started_at, build_id, last_error, updated_at)
		VALUES (?, ?, 'building', '', ?, ?, '', ?)
		ON CONFLICT(root) DO UPDATE SET status='building',
		build_started_at=excluded.build_started_at, build_id=excluded.build_id, last_error='', updated_at=excluded.updated_at
		WHERE workspace_indexes.status != 'building' OR workspace_indexes.updated_at < ?`,
		m.root, m.indexPath, startedAt.Format(time.RFC3339Nano), buildID, startedAt.Format(time.RFC3339Nano), cutoff)
	if err != nil {
		return false, fmt.Errorf("claim workspace index build: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect workspace index build claim: %w", err)
	}
	return rows > 0, nil
}

func (m *Manager) ensureSchema(ctx context.Context) error {
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		return err
	}
	// Each helper opens its own single-connection handle and the write has
	// already been committed by the time this runs, so a Close error carries
	// no recoverable information.
	defer func() { _ = db.Close() }()
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_indexes (
		root TEXT PRIMARY KEY,
		index_path TEXT NOT NULL,
		status TEXT NOT NULL,
		format_version TEXT NOT NULL DEFAULT 'codesearch-v2',
		generation INTEGER NOT NULL DEFAULT 0,
		indexed_at TEXT NOT NULL DEFAULT '',
		build_started_at TEXT NOT NULL DEFAULT '',
		build_id TEXT NOT NULL DEFAULT '',
		last_error TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create workspace index metadata: %w", err)
	}
	for _, migration := range []string{
		`ALTER TABLE workspace_indexes ADD COLUMN format_version TEXT NOT NULL DEFAULT 'codesearch-v2'`,
		`ALTER TABLE workspace_indexes ADD COLUMN generation INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE workspace_indexes ADD COLUMN build_started_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE workspace_indexes ADD COLUMN build_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, migrationErr := db.ExecContext(ctx, migration); migrationErr != nil && !strings.Contains(migrationErr.Error(), "duplicate column name") {
			return fmt.Errorf("migrate workspace index metadata: %w", migrationErr)
		}
	}
	return nil
}

func (m *Manager) state(ctx context.Context) (indexState, error) {
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		return indexState{}, err
	}
	// Each helper opens its own single-connection handle and the write has
	// already been committed by the time this runs, so a Close error carries
	// no recoverable information.
	defer func() { _ = db.Close() }()
	var indexed, indexPath string
	err = db.QueryRowContext(ctx, `SELECT index_path, indexed_at FROM workspace_indexes WHERE root = ?`, m.root).Scan(&indexPath, &indexed)
	if errors.Is(err, sql.ErrNoRows) {
		return indexState{}, nil
	}
	if err != nil {
		return indexState{}, fmt.Errorf("read workspace index metadata: %w", err)
	}
	at, _ := time.Parse(time.RFC3339Nano, indexed)
	return indexState{IndexPath: indexPath, IndexedAt: at}, nil
}

func (m *Manager) setState(ctx context.Context, status string, indexedAt time.Time, lastError string) error {
	db, err := openIndexDB(m.dbPath)
	if err != nil {
		return err
	}
	// Each helper opens its own single-connection handle and the write has
	// already been committed by the time this runs, so a Close error carries
	// no recoverable information.
	defer func() { _ = db.Close() }()
	indexed := ""
	if !indexedAt.IsZero() {
		indexed = indexedAt.Format(time.RFC3339Nano)
	} else if current, stateErr := m.state(ctx); stateErr == nil && !current.IndexedAt.IsZero() {
		indexed = current.IndexedAt.Format(time.RFC3339Nano)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO workspace_indexes (root, index_path, status, indexed_at, last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(root) DO UPDATE SET index_path=excluded.index_path, status=excluded.status,
		indexed_at=excluded.indexed_at, last_error=excluded.last_error, updated_at=excluded.updated_at,
		generation=workspace_indexes.generation + CASE WHEN excluded.status = 'ready' THEN 1 ELSE 0 END`,
		m.root, m.indexPath, status, indexed, lastError, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("write workspace index metadata: %w", err)
	}
	return nil
}

func openIndexDB(path string) (*sql.DB, error) {
	// modernc.org/sqlite only applies pragmas passed via repeated _pragma
	// params; unrecognized keys like _journal_mode/_busy_timeout are
	// silently ignored, which previously left the busy timeout at 0 and
	// caused SQLITE_BUSY on any lock contention instead of a bounded wait.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open workspace index metadata: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func workspaceKey(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:12])
}

func (processRunner) Build(ctx context.Context, root, target string) error {
	_, err := runHelper(ctx, "build", "--root", root, "--index", target)
	return err
}

func (processRunner) Candidates(ctx context.Context, indexPath, pattern string, literal, caseSensitive bool) ([]string, error) {
	args := []string{"candidates", "--index", indexPath, "--pattern", pattern}
	if literal {
		args = append(args, "--literal")
	}
	if caseSensitive {
		args = append(args, "--case-sensitive")
	}
	out, err := runHelper(ctx, args...)
	if err != nil {
		return nil, err
	}
	var files []string
	if err := json.Unmarshal(out, &files); err != nil {
		return nil, fmt.Errorf("decode codesearch candidates: %w", err)
	}
	return files, nil
}

func runHelper(ctx context.Context, args ...string) ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate archied executable: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable, append([]string{"workspace-codesearch"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("codesearch helper: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("run codesearch helper: %w", err)
	}
	return out, nil
}

// BuildIndex creates a codesearch sidecar for root. It is called only inside
// the isolated helper process.
func BuildIndex(ctx context.Context, root, target string) error {
	files, err := WorkspaceFiles(ctx, root)
	if err != nil {
		return err
	}
	w := index.Create(target)
	w.AddRoots([]index.Path{index.MakePath(root)})
	for _, path := range files {
		if err := w.AddFile(path); err != nil {
			continue
		}
	}
	w.Flush()
	return nil
}

// IndexCandidates returns the conservative trigram candidate set. It is
// called only inside the isolated helper process.
func IndexCandidates(indexPath, pattern string, literal, caseSensitive bool) (files []string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			files = nil
			err = fmt.Errorf("open codesearch index: %v", recovered)
		}
	}()
	if literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !caseSensitive && !hasUppercase(pattern) {
		pattern = "(?i:" + pattern + ")"
	}
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, fmt.Errorf("parse search pattern: %w", err)
	}
	ix := index.Open(indexPath)
	ids := ix.PostingQuery(index.RegexpQuery(re))
	files = make([]string, 0, len(ids))
	for _, id := range ids {
		files = append(files, ix.Name(id).String())
	}
	return files, nil
}

// WorkspaceFiles lists searchable workspace files, preferring Git's ignore
// semantics and falling back to a conservative filesystem walk.
func WorkspaceFiles(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err == nil {
		parts := strings.Split(string(out), "\x00")
		files := make([]string, 0, len(parts))
		for _, part := range parts {
			if part != "" {
				path := filepath.Join(root, filepath.FromSlash(part))
				if confined, ok := confinedWorkspaceFile(root, path); ok {
					files = append(files, confined)
				}
			}
		}
		slices.Sort(files)
		return files, nil
	}

	var files []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // an unreadable entry skips that path and continues the walk; a best-effort index must not abort on one bad file
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Type().IsRegular() {
			if confined, ok := confinedWorkspaceFile(root, path); ok {
				files = append(files, confined)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workspace files: %w", err)
	}
	slices.Sort(files)
	return files, nil
}

func confinedWorkspaceFile(root, path string) (string, bool) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	// Reject a symlink at the final component before resolving parent links.
	info, err := os.Lstat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", false
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	resolvedInfo, err := os.Lstat(resolvedPath)
	if err != nil || !resolvedInfo.Mode().IsRegular() {
		return "", false
	}
	return filepath.Clean(resolvedPath), true
}

func hasUppercase(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
