package gateway

import (
	"context"
	"time"
)

// DangerousCommandAuthority is the sandbox-owned authority for operations
// that can destroy process or filesystem state. Adapters only request and
// present approval; they never implement these operations themselves.
type DangerousCommandAuthority interface {
	StopProcess(context.Context, string) error
	Rollback(context.Context, int) (string, error)
	ListCheckpoints(context.Context) ([]CheckpointInfo, error)
}

// CheckpointInfo describes a saved sandbox filesystem checkpoint.
type CheckpointInfo struct {
	Number    int
	Timestamp time.Time
	Label     string
	Size      string
}
