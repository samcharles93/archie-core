package webui

import (
	"encoding/json"
	"net/http"
	"time"

	controlpb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

// auditEntryView is one changed field of one record. An absent side of the
// change is JSON null.
type auditEntryView struct {
	ID        int64           `json:"id"`
	Table     string          `json:"table"`
	RecordKey string          `json:"record_key"`
	Field     string          `json:"field"`
	OldValue  json.RawMessage `json:"old_value"`
	NewValue  json.RawMessage `json:"new_value"`
	Version   int64           `json:"version"`
	Actor     string          `json:"actor"`
	Source    string          `json:"source"`
	At        *time.Time      `json:"at,omitempty"`
}

// handleAudit is GET /api/control-plane/audit?table=T&key=K1&key=K2: the
// field-level audit of the named records of one table, newest first. A page
// passes the records it shows, so its audit list is scoped to that page.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.ControlPlane == nil {
		http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
		return
	}
	query := r.URL.Query()
	response, err := s.ControlPlane.Audit(r.Context(), &controlpb.AuditRequest{Table: query.Get("table"), RecordKeys: query["key"]})
	if err != nil {
		writeControlPlaneError(w, err)
		return
	}
	entries := make([]auditEntryView, 0, len(response.Entries))
	for _, entry := range response.Entries {
		view := auditEntryView{
			ID: entry.Id, Table: entry.Table, RecordKey: entry.RecordKey, Field: entry.Field,
			OldValue: jsonOrNull(entry.OldValueJson), NewValue: jsonOrNull(entry.NewValueJson),
			Version: entry.Version, Actor: entry.Actor, Source: entry.Source,
		}
		if entry.At != nil {
			at := entry.At.AsTime()
			view.At = &at
		}
		entries = append(entries, view)
	}
	writeJSON(w, map[string]any{"entries": entries})
}

func jsonOrNull(value []byte) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("null")
	}
	return value
}
