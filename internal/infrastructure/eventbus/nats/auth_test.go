package nats

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	natsio "github.com/nats-io/nats.go"
)

type taskSeven struct{}

func (taskSeven) TaskSubjects(_ context.Context, taskID int64, credential string) (TaskSubjects, error) {
	if taskID != 7 || credential != "cred-7" {
		return TaskSubjects{}, errors.New("unknown")
	}
	return TaskSubjects{Publish: []string{"archie.agent.7.events"}, Subscribe: []string{"archie.taskrun.7"}}, nil
}

// TestBrokerScopesTaskCredentials pins that the instance token reaches
// everything and a task's run credential reaches only its own subjects, on a
// real embedded broker.
func TestBrokerScopesTaskCredentials(t *testing.T) {
	srv, err := StartEmbedded(context.Background(), EmbeddedOptions{Token: "instance", StoreDir: t.TempDir(), Tasks: taskSeven{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown()

	tests := []struct {
		name     string
		opts     []natsio.Option
		connects bool
		allowed  []string // "pub:" or "sub:" subjects that must work
		refused  []string // that must be refused
	}{
		{
			"instance token reaches everything",
			[]natsio.Option{natsio.Token("instance")},
			true,
			[]string{"pub:archie.taskrun.8", "sub:_INBOX.>", "js:"},
			nil,
		},
		{
			"task credential reaches its own subjects only",
			[]natsio.Option{natsio.UserInfo("task-7", "cred-7"), natsio.CustomInboxPrefix(TaskInboxPrefix(7))},
			true,
			[]string{"pub:archie.agent.7.events", "sub:archie.taskrun.7", "sub:" + TaskInboxPrefix(7) + ".x"},
			[]string{"pub:archie.taskrun.8", "pub:archie.agent.8.events", "sub:archie.taskrun.8", "sub:_INBOX.>"},
		},
		{"credential for another task", []natsio.Option{natsio.UserInfo("task-8", "cred-7")}, false, nil, nil},
		{"unknown credential", []natsio.Option{natsio.UserInfo("task-7", "guess")}, false, nil, nil},
		{"wrong instance token", []natsio.Option{natsio.Token("guess")}, false, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := make(chan string, 16)
			opts := append(slices.Clone(tt.opts), natsio.ErrorHandler(func(_ *natsio.Conn, _ *natsio.Subscription, err error) {
				violations <- err.Error()
			}))
			nc, err := natsio.Connect(srv.ClientURL(), opts...)
			if !tt.connects {
				if err == nil {
					nc.Close()
					t.Fatal("connected; want refused")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer nc.Close()
			for _, op := range tt.allowed {
				if v := attempt(t, nc, op, violations); v != "" {
					t.Errorf("%s refused: %s", op, v)
				}
			}
			for _, op := range tt.refused {
				if v := attempt(t, nc, op, violations); !strings.Contains(v, "Permissions Violation") {
					t.Errorf("%s allowed; want refused", op)
				}
			}
		})
	}
}

// attempt performs one "pub:", "sub:" or "js:" operation and returns the permission
// violation it drew, if any.
func attempt(t *testing.T, nc *natsio.Conn, op string, violations chan string) string {
	t.Helper()
	kind, subject, _ := strings.Cut(op, ":")
	switch kind {
	case "js":
		// The daemon's task queue lives in JetStream.
		js, err := nc.JetStream()
		if err == nil {
			_, err = js.AccountInfo()
		}
		if err != nil {
			return err.Error()
		}
	case "pub":
		_ = nc.Publish(subject, []byte("x"))
	default:
		sub, err := nc.SubscribeSync(subject)
		if err != nil {
			return err.Error()
		}
		defer func() { _ = sub.Unsubscribe() }()
	}
	if err := nc.FlushTimeout(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case v := <-violations:
		return v
	case <-time.After(100 * time.Millisecond):
		return ""
	}
}
