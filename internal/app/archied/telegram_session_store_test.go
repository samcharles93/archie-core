package archied

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestTelegramConversationStoreUsesFreshSQLiteWithoutReadingTaskLog(t *testing.T) {
	taskLogPath := filepath.Join(t.TempDir(), "tasks.db")
	taskLog := []byte("not a SQLite database")
	if err := os.WriteFile(taskLogPath, taskLog, 0o600); err != nil {
		t.Fatalf("write task log fixture: %v", err)
	}

	sessions, err := makeTelegramSessionStore(config.Config{
		DBPath: taskLogPath,
	})
	if err != nil {
		t.Fatalf("makeTelegramSessionStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })

	if got, want := fmt.Sprintf("%T", sessions), "*gateway.sqliteSessionStore"; got != want {
		t.Fatalf("conversation store type = %q, want %q", got, want)
	}
	if _, err := os.Stat(conversationDBPath(taskLogPath)); err != nil {
		t.Fatalf("stat conversation database: %v", err)
	}
	gotTaskLog, err := os.ReadFile(taskLogPath)
	if err != nil {
		t.Fatalf("read task log fixture: %v", err)
	}
	if !bytes.Equal(gotTaskLog, taskLog) {
		t.Fatalf("task log changed during conversation-store cutover: got %q, want %q", gotTaskLog, taskLog)
	}
}
