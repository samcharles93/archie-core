package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type fakeStore struct {
	counts map[string]int
	err    error
}

func (f *fakeStore) StatusCounts(ctx context.Context) (map[string]int, error) {
	return f.counts, f.err
}

func fakeLLM(ctx context.Context, msg Message) (string, error) {
	return "llm: " + msg.Text, nil
}

func TestRouteStatusWithTasks(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{"queued": 2, "pr_open": 1}}, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "/status"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "queued: 2") || !strings.Contains(reply, "pr_open: 1") {
		t.Errorf("reply missing counts: %q", reply)
	}
}

func TestRouteStatusAtMention(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{"running": 3}}, nil, "archie")
	reply, err := r.Route(context.Background(), Message{Text: "/status@archie"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "running: 3") {
		t.Errorf("reply missing counts: %q", reply)
	}
}

func TestRouteStatusEmpty(t *testing.T) {
	r := NewRouter(&fakeStore{counts: map[string]int{}}, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "/status"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "No tasks yet." {
		t.Errorf("reply = %q, want %q", reply, "No tasks yet.")
	}
}

func TestRouteStatusError(t *testing.T) {
	r := NewRouter(&fakeStore{err: errors.New("db down")}, nil, "test")
	if _, err := r.Route(context.Background(), Message{Text: "/status"}); err == nil {
		t.Error("expected error from Route when StatusCounts fails")
	}
}

func TestRouteNonCommandWithLLM(t *testing.T) {
	r := NewRouter(nil, fakeLLM, "test")
	reply, err := r.Route(context.Background(), Message{Text: "hello"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if reply != "llm: hello" {
		t.Errorf("reply = %q, want %q", reply, "llm: hello")
	}
}

func TestRouteNonCommandWithoutLLM(t *testing.T) {
	r := NewRouter(nil, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "hello"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "/status") {
		t.Errorf("reply = %q, want mention of /status", reply)
	}
}

func TestRouterRejectsUnknownCommand(t *testing.T) {
	r := NewRouter(nil, nil, "test")
	reply, err := r.Route(context.Background(), Message{Text: "/frobnicate"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "/status") {
		t.Errorf("reply = %q, want mention of /status", reply)
	}
}

// -- ModelManager fakes for /model and /models tests --

type fakeModelManager struct {
	models      []string
	activeModel string
	setErr      error
}

func (f *fakeModelManager) Models() []string {
	return f.models
}

func (f *fakeModelManager) ActiveModel() string {
	return f.activeModel
}

func (f *fakeModelManager) SetActiveModel(_ context.Context, ref string) error {
	if f.setErr != nil {
		return f.setErr
	}
	for _, m := range f.models {
		if m == ref {
			f.activeModel = ref
			return nil
		}
	}
	return fmt.Errorf("unknown model: %s", ref)
}

// -- /models tests --

func TestRouteModels(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b", "c/d"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/models"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") || !strings.Contains(reply, "c/d") {
		t.Errorf("reply missing model names: %q", reply)
	}
	if strings.Contains(reply, "active") {
		t.Errorf("reply should not flag an active model when none is set: %q", reply)
	}
}

func TestRouteModelsWithActive(t *testing.T) {
	mgr := &fakeModelManager{
		models:      []string{"a/b", "c/d"},
		activeModel: "c/d",
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/models"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") || !strings.Contains(reply, "c/d (active)") {
		t.Errorf("reply should flag active model: %q", reply)
	}
}

func TestRouteModelsAtMention(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/models@test-gw"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") {
		t.Errorf("reply missing model names: %q", reply)
	}
}

func TestRouteModelsEmpty(t *testing.T) {
	mgr := &fakeModelManager{}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/models"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "No models") {
		t.Errorf("reply = %q, want 'No models' message", reply)
	}
}

func TestRouteModelsNoManager(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/models"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured' message", reply)
	}
}

// -- /model tests --

func TestRouteModelSwitch(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b", "c/d"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") || !strings.Contains(reply, "set to") {
		t.Errorf("reply should confirm model switch: %q", reply)
	}
	if mgr.activeModel != "a/b" {
		t.Errorf("active model = %q, want %q", mgr.activeModel, "a/b")
	}
}

func TestRouteModelAtMention(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model@test-gw a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "a/b") {
		t.Errorf("reply should confirm model switch: %q", reply)
	}
}

func TestRouteModelUnknown(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model unknown/unknown"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Cannot switch") {
		t.Errorf("reply should mention cannot switch: %q", reply)
	}
}

func TestRouteModelNoArg(t *testing.T) {
	mgr := &fakeModelManager{
		models: []string{"a/b", "c/d"},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Models = mgr
	reply, err := r.Route(context.Background(), Message{Text: "/model"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") || !strings.Contains(reply, "a/b") || !strings.Contains(reply, "c/d") {
		t.Errorf("reply should show usage and available models: %q", reply)
	}
}

func TestRouteModelNoManager(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/model a/b"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured' message", reply)
	}
}

// -- TaskCreator fakes for /spawn tests --

type fakeTaskCreator struct {
	tasks    []string
	createFn func(ctx context.Context, title string) (int64, error)
}

func (f *fakeTaskCreator) CreateTask(ctx context.Context, title string) (int64, error) {
	f.tasks = append(f.tasks, title)
	if f.createFn != nil {
		return f.createFn(ctx, title)
	}
	return int64(len(f.tasks)), nil
}

func TestRouteSpawnCreatesTask(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn Fix the login bug"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") || !strings.Contains(reply, "1") {
		t.Errorf("reply should confirm task creation: %q", reply)
	}
	if len(tc.tasks) != 1 || tc.tasks[0] != "Fix the login bug" {
		t.Errorf("tasks = %v, want [Fix the login bug]", tc.tasks)
	}
}

func TestRouteSpawnAtMention(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "archie-bot")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn@archie-bot Deploy the release"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Created task") {
		t.Errorf("reply should confirm creation: %q", reply)
	}
}

func TestRouteSpawnNoTitle(t *testing.T) {
	tc := &fakeTaskCreator{}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Usage") {
		t.Errorf("reply should show usage: %q", reply)
	}
	if len(tc.tasks) != 0 {
		t.Errorf("no task should be created without a title")
	}
}

func TestRouteSpawnNoManager(t *testing.T) {
	r := NewRouter(nil, nil, "test-gw")
	reply, err := r.Route(context.Background(), Message{Text: "/spawn something"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "not configured") {
		t.Errorf("reply = %q, want 'not configured'", reply)
	}
}

func TestRouteSpawnHandlesCreationError(t *testing.T) {
	tc := &fakeTaskCreator{
		createFn: func(ctx context.Context, title string) (int64, error) {
			return 0, fmt.Errorf("store unavailable")
		},
	}
	r := NewRouter(nil, nil, "test-gw")
	r.Tasks = tc
	reply, err := r.Route(context.Background(), Message{Text: "/spawn Break things"})
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if !strings.Contains(reply, "Failed") {
		t.Errorf("reply should mention failure: %q", reply)
	}
}
