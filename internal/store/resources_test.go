package store

import (
	"errors"
	"testing"
	"time"
)

func TestPutResourceRejectsStaleVersion(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	first, err := s.PutResource(ctx, ResourceWrite{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "a", Source: "test", RequestID: "r1", ExpectedVersion: 0, At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutResource(ctx, ResourceWrite{Kind: "settings", Value: []byte(`{"v":2}`), Actor: "a", Source: "test", RequestID: "r2", ExpectedVersion: 0, At: time.Now()})
	if !errors.Is(err, ErrResourceVersionConflict) {
		t.Fatalf("PutResource() error = %v, want ErrResourceVersionConflict (current %d)", err, first.Version)
	}
}

func TestPutResourceDuplicateRequestIsIdempotent(t *testing.T) {
	s := OpenTest(t)
	defer s.Close()
	ctx := t.Context()
	write := ResourceWrite{Kind: "settings", Value: []byte(`{"v":1}`), Actor: "a", Source: "test", RequestID: "same", ExpectedVersion: 0, At: time.Now()}
	first, err := s.PutResource(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	write.Value = []byte(`{"v":999}`)
	second, err := s.PutResource(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != second.Version || string(second.Value) != string(first.Value) {
		t.Fatalf("duplicate = (%d, %s), want (%d, %s)", second.Version, second.Value, first.Version, first.Value)
	}
}
