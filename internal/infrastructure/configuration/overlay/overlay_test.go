package overlay

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func openTest(t *testing.T) (*Store, context.Context) {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "config.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, context.Background()
}

func TestSetGetSnapshotRoundTrip(t *testing.T) {
	s, ctx := openTest(t)

	for _, tc := range []struct {
		key, value string
	}{
		{"budgets.max_steps", "12"},
		{"label", `"archie"`},
		{"containers.image", `"archie-agent:test"`},
		{"web.listen", `":8643"`},
	} {
		if err := s.Set(ctx, tc.key, tc.value, "test"); err != nil {
			t.Fatalf("Set(%s): %v", tc.key, err)
		}
	}

	snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}

	budgets, ok := snap["budgets"].(map[string]any)
	if !ok {
		t.Fatalf("budgets = %#v, want map", snap["budgets"])
	}
	if got := budgets["max_steps"]; !reflect.DeepEqual(got, float64(12)) {
		t.Errorf("budgets.max_steps = %#v, want float64(12)", got)
	}
	if got := snap["label"]; got != "archie" {
		t.Errorf("label = %#v, want archie", got)
	}
	containers, ok := snap["containers"].(map[string]any)
	if !ok {
		t.Fatalf("containers = %#v, want map", snap["containers"])
	}
	if got := containers["image"]; got != "archie-agent:test" {
		t.Errorf("containers.image = %#v, want archie-agent:test", got)
	}
	web, ok := snap["web"].(map[string]any)
	if !ok {
		t.Fatalf("web = %#v, want map", snap["web"])
	}
	if got := web["listen"]; got != ":8643" {
		t.Errorf("web.listen = %#v, want :8643", got)
	}
}

func TestSetUpsertsAndDeleteRestores(t *testing.T) {
	s, ctx := openTest(t)

	if err := s.Set(ctx, "label", `"first"`, "test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set(ctx, "label", `"second"`, "test"); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap["label"] != "second" {
		t.Fatalf("after upsert, label = %#v, want second", snap["label"])
	}

	if err := s.Delete(ctx, "label"); err != nil {
		t.Fatal(err)
	}
	snap, err = s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 0 {
		t.Fatalf("after delete, snapshot = %#v, want empty", snap)
	}
}

func TestSetRefusesDeniedKeys(t *testing.T) {
	s, ctx := openTest(t)
	for key := range DeniedKeys {
		if err := s.Set(ctx, key, `"x"`, "test"); err == nil {
			t.Errorf("Set(%s) succeeded, want denial", key)
		}
	}
}

func TestNestBuildsNestedMaps(t *testing.T) {
	root := map[string]any{}
	if err := Nest(root, "a.b.c", 1); err != nil {
		t.Fatal(err)
	}
	if err := Nest(root, "a.b.d", "x"); err != nil {
		t.Fatal(err)
	}
	a, ok := root["a"].(map[string]any)
	if !ok {
		t.Fatalf("root[a] = %#v, want map", root["a"])
	}
	ab, ok := a["b"].(map[string]any)
	if !ok {
		t.Fatalf("root[a][b] = %#v, want map", a["b"])
	}
	if ab["c"] != 1 || ab["d"] != "x" {
		t.Fatalf("nested map = %#v", root)
	}
	if err := Nest(root, "", 1); err == nil {
		t.Fatal("empty key accepted")
	}
}

func TestOpenIdempotentAndVersioned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.sqlite")
	s1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = s1.Close()
	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()
}
