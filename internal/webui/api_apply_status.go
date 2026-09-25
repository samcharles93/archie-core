package webui

import (
	"context"
	"net/http"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/applystatus"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
)

// The readings an operator acts on. A process is current when it is running
// what it last reported, failed when it rejected what the store holds, and
// unknown when it has stopped re-stamping: a record that outlived its process
// must never read as current (docs/prds/control-plane-apply-status.md).
const (
	applyStateCurrent = "current"
	applyStateFailed  = "failed"
	applyStateUnknown = "unknown"
)

type applyStatusRecord struct {
	Process        string    `json:"process"`
	Kind           string    `json:"kind"`
	AppliedVersion int64     `json:"applied_version"`
	Error          string    `json:"error,omitempty"`
	ReportedAt     time.Time `json:"reported_at"`
	State          string    `json:"state"`
}

type applyStatusView struct {
	Records []applyStatusRecord `json:"records"`
	// Processes is every name a record may carry, so the page can show a
	// process that has never reported at all rather than omitting the column.
	Processes []string `json:"processes"`
}

// handleApplyStatus reports which version of each control-plane resource each
// process is running. A process with no apply-status wiring renders an empty
// page rather than failing: that is a deployment fact, not an error.
func (s *Server) handleApplyStatus(w http.ResponseWriter, r *http.Request) {
	view, err := s.readApplyStatus(r.Context())
	if err != nil {
		http.Error(w, "apply status unavailable", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, view)
}

func (s *Server) readApplyStatus(ctx context.Context) (applyStatusView, error) {
	view := applyStatusView{Records: []applyStatusRecord{}, Processes: applystatus.Processes()}
	if s.ApplyStatus == nil {
		return view, nil
	}
	records, err := s.ApplyStatus.ListApplyStatus(ctx)
	if err != nil {
		return view, err
	}
	now := s.clock()
	for _, record := range records {
		view.Records = append(view.Records, applyStatusRecord{
			Process: record.Process, Kind: record.Kind, AppliedVersion: record.AppliedVersion,
			Error: record.Error, ReportedAt: record.ReportedAt, State: applyState(record, now),
		})
	}
	return view, nil
}

// applyState reads staleness before the error: a process that stopped
// reporting is unknown whatever its last report said, because that report
// describes a process that may no longer exist.
func applyState(record storecontract.ApplyStatus, now time.Time) string {
	switch {
	case applystatus.Stale(record.ReportedAt, now):
		return applyStateUnknown
	case record.Error != "":
		return applyStateFailed
	default:
		return applyStateCurrent
	}
}

func (s *Server) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}
