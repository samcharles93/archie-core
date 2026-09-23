package webui

import (
	"net/http"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/taskstate"
)

// changeStatusMeta describes how to present one changes_captured status value.
// The IDs are the producer's own constants (internal/domain/workflow/task), so
// the dashboard never spells them out and a rename reaches the browser instead
// of turning every row into a raw status code.
type changeStatusMeta struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// taskMetaView is the whole GET /api/task-meta payload. It is served as one
// struct rather than an inline map so the wire shape is declared in one place
// and pinned against the dashboard's freeze-dried snapshot by a fixture test.
type taskMetaView struct {
	Statuses       []taskstate.StatusMeta `json:"statuses"`
	Actions        []taskstate.ActionMeta `json:"actions"`
	ChangeStatuses []changeStatusMeta     `json:"change_statuses"`
	ConfigSchema   string                 `json:"config_schema"`
}

// buildTaskMeta derives the lifecycle presentation catalog: the status
// vocabulary (with label, pill severity, "needs you" grouping), the operator
// action controls (with label, button variant, confirm prompt), the
// changes_captured status labels and the schema stamp a config_captured
// payload carries. Everything the dashboard can present that also exists on
// the server is derived here, so the frontend never has to keep a hand-synced
// copy of the vocabulary or the control set.
func buildTaskMeta() taskMetaView {
	return taskMetaView{
		Statuses: taskstate.Statuses(),
		Actions:  taskstate.ActionCatalog(),
		ChangeStatuses: []changeStatusMeta{
			{ID: task.ChangeAdded, Label: "Added"},
			{ID: task.ChangeModified, Label: "Modified"},
			{ID: task.ChangeDeleted, Label: "Deleted"},
			{ID: task.ChangeRenamed, Label: "Renamed"},
			{ID: task.ChangeTypeChanged, Label: "Type changed"},
		},
		ConfigSchema: events.ConfigCapturedSchema,
	}
}

// handleTaskMeta serves the catalog. See buildTaskMeta for what it carries.
func (s *Server) handleTaskMeta(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, buildTaskMeta())
}
