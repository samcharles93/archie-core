package memory

import (
	"fmt"
	"time"

	domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"
	"github.com/samcharles93/archie-core/internal/infrastructure/memory/builtin"
)

// historyFileName is the second document in each scope's directory: every
// state the scope's records no longer live in -- superseded by an Update, or
// removed by a Forget -- so a record's provenance outlives its content
const historyFileName = "HISTORY.md"

// historySectionName is the single section every retained state is appended
// to. One section is enough: each block's marker carries its record id, so
// grouping by kind would be a second index over the same file.
const historySectionName = "history"

// historyMaxFileBytes bounds each scope's HISTORY.md.
const historyMaxFileBytes = 4 * 1024 * 1024

// appendHistory appends one retained state -- the record as it was while
// live, plus when it stopped being live and whether it was deleted -- to the
// scope's history document. Content is passed beside m because the marker
// carries provenance, never the record's bytes.
func appendHistory(h *builtin.Store, m markerData, content string, at time.Time, deleted bool) error {
	m.SupersededAt = &at
	m.Deleted = deleted
	block, err := renderBlock(m, content)
	if err != nil {
		return err
	}
	if _, err := h.Add(historySectionName, block); err != nil {
		return fmt.Errorf("memory: builtin engine: retaining the superseded state of record %q: %w", m.ID, err)
	}
	return nil
}

// revisionsOf returns one record's retained states, oldest first.
func revisionsOf(h *builtin.Store, scope domainmemory.Scope, id domainmemory.RecordID) []domainmemory.Revision {
	var revisions []domainmemory.Revision
	for _, block := range parseBlocks(h.Render()) {
		if block.marker.ID != string(id) {
			continue
		}
		var supersededAt time.Time
		if block.marker.SupersededAt != nil {
			supersededAt = *block.marker.SupersededAt
		}
		revisions = append(revisions, domainmemory.Revision{
			Record:       block.record(scope),
			SupersededAt: supersededAt,
			Deleted:      block.marker.Deleted,
		})
	}
	return revisions
}
