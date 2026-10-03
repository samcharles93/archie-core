package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
)

// memoryRecordsPerScope bounds how many records of each scope the read path
// asks the engine for. It is a per-scope limit, not a total: Subject.Scopes()
// returns at most four scopes, so the worst case is four times this many
// records before the byte cap below trims further.
const memoryRecordsPerScope = 20

// memoryBlockByteCap bounds the <memory> block; a record that would exceed it
// is dropped and logged.
const memoryBlockByteCap = 8192

// MemoryStore is the read surface prepareTurn needs from a memory engine: a
// scope-only Query, nothing else. Narrowed from domainmemory.Store (which
// also carries the write operations slice 4 uses) so this package's read
// path cannot accidentally reach for Create/Update/Forget.
type MemoryStore interface {
	Query(ctx context.Context, q domainmemory.Query) ([]domainmemory.Record, error)
}

// renderMemory builds the turn's <memory> block from a scope-only Query over
// subject.Scopes(), bounded by memoryRecordsPerScope per scope and
// memoryBlockByteCap overall. Any engine failure, including a panic, yields an
// empty block: memory never fails a turn.
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

	// Query each scope separately so each gets its own limit.
	var records []domainmemory.Record
	for _, scope := range subject.Scopes() {
		heads, err := store.Query(ctx, domainmemory.Query{
			Scopes: []domainmemory.Scope{scope},
			Limit:  memoryRecordsPerScope,
		})
		if err != nil {
			if log != nil {
				log.Warn("chat memory read failed; continuing without a memory block", "err", err)
			}
			return ""
		}
		records = append(records, heads...)
	}
	return renderMemoryRecords(records, log)
}

// renderMemoryRecords formats one line per record, dropping any that would
// push the escaped block past memoryBlockByteCap.
func renderMemoryRecords(records []domainmemory.Record, log *slog.Logger) string {
	var b strings.Builder
	escapedLen := 0
	dropped := 0
	for _, record := range records {
		line := fmt.Sprintf("- [%s] (%s, %s) %s\n", record.ID, record.Scope.Kind, record.Kind, record.Content)
		next := escapedLen + len(escapeXML(line))
		if next > memoryBlockByteCap {
			dropped++
			continue
		}
		escapedLen = next
		b.WriteString(line)
	}
	if dropped > 0 && log != nil {
		log.Warn("chat memory block exceeded its render cap; some records were dropped",
			"cap_bytes", memoryBlockByteCap, "dropped", dropped, "rendered", len(records)-dropped)
	}
	return strings.TrimRight(b.String(), "\n")
}

// resolveSubject builds the turn's memory Subject from BotUser and the
// UserIdentity resolver.
func (r *TurnRunner) resolveSubject(in Inbound) domainmemory.Subject {
	subject := domainmemory.Subject{AgentID: domainmemory.AgentID(r.BotUser)}
	if r.UserIdentity == nil {
		return subject
	}
	if id, ok := r.UserIdentity(in); ok {
		subject.UserID = id
	}
	return subject
}
