// Package usage records model calls for attribution and chargeback.
package usage

import (
	"context"
	"strings"
	"time"
)

// Source is what made a model call.
type Source string

const (
	SourceTask    Source = "task"
	SourceChat    Source = "chat"
	SourceCurator Source = "curator"
)

// Record is one model call. A task's record belongs to the task's org,
// whatever Org says.
type Record struct {
	Org          string
	Source       Source
	TaskID       int64
	Attempt      int
	Workflow     string
	Step         string
	Alias        string
	Provider     string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	At           time.Time
}

// Recorder appends usage records.
type Recorder interface {
	RecordUsage(ctx context.Context, r Record) error
}

// WithRef fills Provider and Model from a "provider/model" ref.
func (r Record) WithRef(ref string) Record {
	r.Provider, r.Model, _ = strings.Cut(ref, "/")
	return r
}
