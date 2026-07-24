package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/container"
)

func TestBootTaskID(t *testing.T) {
	log := slog.New(slog.DiscardHandler)

	t.Run("no task.json — shared pool mode", func(t *testing.T) {
		id, ok := bootTaskID(t.TempDir(), log)
		if ok || id != 0 {
			t.Fatalf("bootTaskID() = (%d, %v), want (0, false)", id, ok)
		}
	})

	t.Run("valid task.json — dedicated per-task mode", func(t *testing.T) {
		dir := t.TempDir()
		data, err := json.Marshal(container.TaskPayload{ID: 42, Owner: "acme", Repo: "widget"})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "task.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		id, ok := bootTaskID(dir, log)
		if !ok || id != 42 {
			t.Fatalf("bootTaskID() = (%d, %v), want (42, true)", id, ok)
		}
	})

	t.Run("malformed task.json falls back to shared pool mode", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "task.json"), []byte("not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, ok := bootTaskID(dir, log)
		if ok || id != 0 {
			t.Fatalf("bootTaskID() = (%d, %v), want (0, false)", id, ok)
		}
	})

	t.Run("zero ID falls back to shared pool mode", func(t *testing.T) {
		dir := t.TempDir()
		data, _ := json.Marshal(container.TaskPayload{Owner: "acme", Repo: "widget"})
		if err := os.WriteFile(filepath.Join(dir, "task.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		id, ok := bootTaskID(dir, log)
		if ok || id != 0 {
			t.Fatalf("bootTaskID() = (%d, %v), want (0, false)", id, ok)
		}
	})
}

func TestNATSConnectionSettings(t *testing.T) {
	getenv := func(name string) string {
		switch name {
		case "NATS_URL":
			return "nats://nats.example:4222"
		case "NATS_TOKEN":
			return "test-nats-token"
		default:
			return ""
		}
	}

	t.Run("container environment", func(t *testing.T) {
		url, token := natsConnectionSettings("", getenv)
		if url != "nats://nats.example:4222" || token != "test-nats-token" {
			t.Fatalf("natsConnectionSettings() = (%q, %q)", url, token)
		}
	})

	t.Run("flag URL takes precedence", func(t *testing.T) {
		url, token := natsConnectionSettings("nats://flag.example:4222", getenv)
		if url != "nats://flag.example:4222" || token != "test-nats-token" {
			t.Fatalf("natsConnectionSettings() = (%q, %q)", url, token)
		}
	})
}
