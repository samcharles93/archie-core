package memory

import domainmemory "github.com/samcharles93/archie-core/internal/domain/memory"

// The content scanner moved into the memory engine family
// (internal/domain/memory/scanner.go, docs/prds/memory-engine-unification.md
// §7), where the engine that persists memory applies it. This package's
// legacy Manager still needs the same types until it is deleted in slice 5 of
// that PRD, so they are re-exported here rather than duplicated: the
// behaviour, the patterns and the tests stay exactly where the decision put
// them.
//
// Delete this file with internal/memory.
type (
	// ThreatLevel is domainmemory.ThreatLevel.
	ThreatLevel = domainmemory.ThreatLevel
	// ThreatResult is domainmemory.ThreatResult.
	ThreatResult = domainmemory.ThreatResult
	// Scanner is domainmemory.Scanner.
	Scanner = domainmemory.Scanner
	// DefaultScanner is domainmemory.DefaultScanner.
	DefaultScanner = domainmemory.DefaultScanner
)

const (
	// ThreatNone is domainmemory.ThreatNone.
	ThreatNone = domainmemory.ThreatNone
	// ThreatWarn is domainmemory.ThreatWarn.
	ThreatWarn = domainmemory.ThreatWarn
	// ThreatBlock is domainmemory.ThreatBlock.
	ThreatBlock = domainmemory.ThreatBlock
)
