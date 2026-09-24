package legacyread

import (
	"path/filepath"
	"testing"
)

func TestHasData(t *testing.T) {
	emptyGateway := `CREATE TABLE sessions (session_id TEXT PRIMARY KEY);
CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT);`
	tests := []struct {
		name   string
		path   func(t *testing.T) string
		tables []Table
		want   bool
	}{
		{"missing file", func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent.sqlite") }, GatewayTables, false},
		{"tables without rows", func(t *testing.T) string { return fixture(t, "gw.sqlite", emptyGateway) }, GatewayTables, false},
		{"rows present", func(t *testing.T) string { return fixture(t, "gw.sqlite", gatewayDDL) }, GatewayTables, true},
		{"rows in the state store", func(t *testing.T) string { return fixture(t, "archie.db", stateStoreDDL) }, StateStoreTables, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HasData(t.Context(), tt.path(t), tt.tables)
			if err != nil {
				t.Fatalf("HasData: %v", err)
			}
			if got != tt.want {
				t.Fatalf("HasData = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSequenceOfMessages(t *testing.T) {
	path := fixture(t, "gw.sqlite", gatewayDDL)
	exec(t, path, `DELETE FROM messages WHERE message_id = 'm2'`)
	src := openSource(t, path)
	got, err := src.SequenceOf(t.Context(), "messages")
	if err != nil {
		t.Fatalf("SequenceOf: %v", err)
	}
	if want := (Sequence{Table: "messages", MaxID: 1, Next: 3}); got != want {
		t.Fatalf("SequenceOf(messages) = %+v, want %+v", got, want)
	}
}
