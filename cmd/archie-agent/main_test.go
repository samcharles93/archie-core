package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/agentworker"
)

func TestRunCommandPreservesCLIExitSemantics(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		getenv   func(string) string
		worker   workerRunner
		wantCode int
	}{
		{name: "help", args: []string{"-h"}},
		{name: "flag parse error", args: []string{"-unknown"}, wantCode: 2},
		{name: "missing NATS URL", wantCode: 1},
		{
			name: "worker failure",
			args: []string{"-nats-url", "nats://test"},
			worker: func(context.Context, agentworker.Settings, *slog.Logger) error {
				return errors.New("worker failed")
			},
			wantCode: 1,
		},
		{
			name: "success",
			args: []string{"-nats-url", "nats://test"},
			worker: func(context.Context, agentworker.Settings, *slog.Logger) error {
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			getenv := test.getenv
			if getenv == nil {
				getenv = func(string) string { return "" }
			}
			worker := test.worker
			if worker == nil {
				worker = func(context.Context, agentworker.Settings, *slog.Logger) error {
					t.Fatal("worker called")
					return nil
				}
			}
			if got := runCommand(test.args, getenv, &bytes.Buffer{}, worker); got != test.wantCode {
				t.Fatalf("runCommand() = %d, want %d", got, test.wantCode)
			}
		})
	}
}

func TestRunCommandRequiresNATSURL(t *testing.T) {
	var stderr bytes.Buffer
	called := false
	code := runCommand(nil, func(string) string { return "" }, &stderr, func(context.Context, agentworker.Settings, *slog.Logger) error {
		called = true
		return nil
	})
	if code != 1 || called {
		t.Fatalf("(code, worker called) = (%d, %v), want (1, false)", code, called)
	}
	for _, want := range []string{"error: -nats-url or NATS_URL is required", "Usage of archie-agent:"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q does not contain %q", stderr.String(), want)
		}
	}
}

func TestRunCommandMapsWorkerOutcomeToExitStatus(t *testing.T) {
	workerErr := errors.New("startup failed")
	tests := []struct {
		name      string
		workerErr error
		wantCode  int
	}{
		{name: "success"},
		{name: "worker failure", workerErr: workerErr, wantCode: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runCommand([]string{"-nats-url", "nats://flag", "-consumer", "custom"}, func(string) string { return "token" }, &stderr, func(_ context.Context, settings agentworker.Settings, _ *slog.Logger) error {
				if settings.NATSURL != "nats://flag" || settings.NATSToken != "token" || settings.Consumer != "custom" {
					t.Fatalf("settings = %#v", settings)
				}
				return test.workerErr
			})
			if code != test.wantCode {
				t.Fatalf("code = %d, want %d", code, test.wantCode)
			}
			if test.workerErr != nil && stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want command not to duplicate application logging", stderr.String())
			}
		})
	}
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
