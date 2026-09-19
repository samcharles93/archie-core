package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// memoryRecordsPerScope bounds how many records of each scope the read path
// asks the engine for. It is a per-scope limit, not a total: Subject.Scopes()
// returns at most four scopes, so the worst case is four times this many
// records before the byte cap below trims further.
const memoryRecordsPerScope = 20

// memoryBlockByteCap bounds the rendered <memory> block's size. This is a
// size guard, not a timeout: the read is local and synchronous, and nothing
// here waits on the network. A record that would push the block over the cap
// is dropped (not truncated mid-record) and the drop is logged, so the block
// stays valid content rather than silently corrupted output.
const memoryBlockByteCap = 8192

// MemoryStore is the read surface prepareTurn needs from a memory engine: a
// scope-only Query, nothing else. Narrowed from domainmemory.Store (which
// also carries the write operations slice 4 uses) so this package's read
// path cannot accidentally reach for Create/Update/Forget.
type MemoryStore interface {
	Query(ctx context.Context, q domainmemory.Query) ([]domainmemory.Record, error)
}

// renderMemory builds the turn's <memory> block body: a scope-only Query over
// subject.Scopes() (most specific first, per Subject.Scopes' own doc), bounded
// by memoryRecordsPerScope per scope and memoryBlockByteCap overall.
//
// This is synchronous, total, and never returns an error: docs/prds/
// memory-engine-unification.md §4 requires the chat read path to degrade to
// an empty block on any engine failure, including a panic, rather than fail
// the turn -- a memory read is an enhancement to the prompt, never a
// precondition for answering. The recover here is that contract's only
// enforcement point; every caller below runs inside it.
func renderMemory(ctx context.Context, store MemoryStore, subject domainmemory.Subject, log *slog.Logger) (block string) {
	if store == nil {
		return ""
	}
	defer func() {
		if p := recover(); p != nil {
			if log != nil {
				log.Warn("chat memory read panicked; continuing without a memory block", "panic", p)
			}
			block = ""
		}
	}()

	records, err := store.Query(ctx, domainmemory.Query{
		Scopes: subject.Scopes(),
		Limit:  memoryRecordsPerScope * len(subject.Scopes()),
	})
	if err != nil {
		if log != nil {
			log.Warn("chat memory read failed; continuing without a memory block", "err", err)
		}
		return ""
	}
	return renderMemoryRecords(records, log)
}

// renderMemoryRecords formats records as one line each, addressed by the
// record's own id -- the same id the write path (slice 4) will take a
// memory_edit call by, so the read and write directions share one source of
// truth for addressing. A record whose line would push the block past
// memoryBlockByteCap is dropped and logged rather than truncated mid-line,
// so a partial block is never mistaken for a complete or a corrupted one.
func renderMemoryRecords(records []domainmemory.Record, log *slog.Logger) string {
	var b strings.Builder
	dropped := 0
	for _, record := range records {
		line := fmt.Sprintf("- [%s] (%s, %s) %s\n", record.ID, record.Scope.Kind, record.Kind, record.Content)
		if b.Len()+len(line) > memoryBlockByteCap {
			dropped++
			continue
		}
		b.WriteString(line)
	}
	if dropped > 0 && log != nil {
		log.Warn("chat memory block exceeded its render cap; some records were dropped",
			"cap_bytes", memoryBlockByteCap, "dropped", dropped, "rendered", len(records)-dropped)
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolveSubject builds a turn's memory Subject from the runner's agent
// identity (BotUser, the closest thing to an Agent id the tree records today
// -- see sessioncurator.Adapter) and the channel's UserIdentity resolver. A
// nil resolver, or one that returns false, yields UserID "" -- Subject.Scopes
// then names only agent and global, never a wider fallback
// (docs/prds/memory-engine-unification.md §3, "fail closed").
func (r *TurnRunner) resolveSubject(msg messaging.Message) domainmemory.Subject {
	subject := domainmemory.Subject{AgentID: domainmemory.AgentID(r.BotUser)}
	if r.UserIdentity == nil {
		return subject
	}
	if id, ok := r.UserIdentity(msg); ok {
		subject.UserID = id
	}
	return subject
}
