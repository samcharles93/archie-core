package gateway

import "testing"

// TestSessionStoreConformanceSQLite runs the shared SessionStore contract
// against the SQLite implementation.
func TestSessionStoreConformanceSQLite(t *testing.T) {
	runSessionStoreSuite(t, func(t *testing.T) SessionStore {
		s, err := NewSQLiteSessionStoreMemory()
		if err != nil {
			t.Fatalf("NewSQLiteSessionStoreMemory: %v", err)
		}
		return s
	})
}
