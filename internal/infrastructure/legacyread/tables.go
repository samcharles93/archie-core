package legacyread

func idTable(name string, columns ...string) Table {
	return Table{Name: name, Columns: columns, Key: []string{columns[0]}, OrderBy: columns[0]}
}

// StateStoreTables are the twelve tables of the State Store's archie.db.
var StateStoreTables = []Table{
	idTable("tasks", "id", "owner", "repo", "issue_number", "title", "body", "labels", "status",
		"workflow", "stage", "branch", "plan", "notes", "pr_number", "tokens_used", "iterations",
		"attempt", "park_reason", "watch_comment_id", "park_class", "remediation_rounds", "retry_count",
		"source", "identity", "binding_id", "binding_version", "review_payload",
		"workflow_definition_version", "workflow_definition_digest", "workflow_definition_yaml",
		"created_at", "updated_at", "review_cursor"),
	idTable("transitions", "id", "task_id", "at", "from_status", "to_status", "detail"),
	{
		Name: "events",
		Columns: []string{
			"id", "at", "kind", "task_id", "repo", "issue", "workflow", "stage", "attempt",
			"actor_id", "actor_kind", "principal_id", "detail", "data",
		},
		Key:     []string{"id"},
		OrderBy: "at, id",
	},
	idTable("resources", "kind", "value", "version", "updated_at"),
	idTable("resource_history", "id", "kind", "value", "version", "actor", "source", "request_id",
		"expected_version", "current_version", "at"),
	idTable("channel_status", "id", "name", "state", "detail", "configured", "reload_supported", "observed_at"),
	{
		Name:    "apply_status",
		Columns: []string{"process", "kind", "applied_version", "error", "reported_at"},
		Key:     []string{"process", "kind"},
		OrderBy: "process, kind",
	},
	idTable("config_snapshot", "id", "schema", "document", "published_at"),
	idTable("identities", "id", "kind", "display_name", "lifecycle", "version", "created_at", "updated_at"),
	idTable("identity_aliases", "alias", "identity_id"),
	idTable("identity_events", "id", "identity_id", "event_type", "from_lifecycle", "to_lifecycle",
		"display_name", "actor_id", "source", "request_id", "at"),
	{
		Name:    "identity_subjects",
		Columns: []string{"issuer", "subject", "identity_id", "bound_at"},
		Key:     []string{"issuer", "subject"},
		OrderBy: "issuer, subject",
	},
}

// EDATables are the six app collections of the PocketBase event store.
// PocketBase's own tables (_collections, _params, _superusers, ...) are not
// archie data and are not read.
var EDATables = []Table{
	idTable("captures", "id", "authenticated", "body", "content_type", "headers", "received_at",
		"remote_addr", "source"),
	idTable("mappings", "id", "created_at", "fields", "name", "source_hint", "updated_at"),
	idTable("bindings", "id", "created_at", "mapping", "name", "owner", "repo", "secret", "source",
		"status", "updated_at", "version", "workflow"),
	idTable("binding_dispatches", "id", "binding", "binding_version", "capture", "dispatched_at", "task_id"),
	idTable("playbook_dispatches", "id", "action_id", "dispatched_at", "event_id", "playbook_id",
		"playbook_version"),
	idTable("tool_calls", "id", "args", "attempt", "called_at", "duration_ms", "error", "result",
		"task_id", "tool"),
}

// GatewayTables are the Gateway conversation store's three data tables. Its
// FTS5 index is read through IndexedMessageIDs.
var GatewayTables = []Table{
	idTable("sessions", "session_id", "platform", "bot_user", "channel_id", "thread_id", "title",
		"parent_session_id", "branch_name", "created_at", "last_active_at"),
	idTable("messages", "id", "message_id", "session_id", "source_id", "sender", "text", "ts", "role", "sender_id"),
	idTable("turns", "turn_id", "session_id", "source_id", "status", "attempt", "owner_id",
		"input_message_id", "assistant_message_id", "partial_text", "response_text", "tool_calls",
		"error", "created_at", "updated_at"),
}
