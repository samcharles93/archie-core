package store

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/mapping"
)

func testFields() []mapping.Field {
	return []mapping.Field{
		{Name: "title", Path: "issue.title", Type: mapping.TypeString, Required: true},
		{Name: "count", Path: "count", Type: mapping.TypeNumber},
	}
}

func TestInsertMappingRoundTrips(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertMapping(t.Context(), mapping.Mapping{
		Name:       "sentry issue opened",
		SourceHint: "sentry",
		Fields:     testFields(),
	})
	if err != nil {
		t.Fatalf("InsertMapping: %v", err)
	}
	if id == 0 {
		t.Fatalf("InsertMapping returned zero id")
	}

	got, err := s.GetMapping(t.Context(), id)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got == nil {
		t.Fatalf("GetMapping returned nil for a just-inserted row")
	}
	if got.ID != id || got.Name != "sentry issue opened" || got.SourceHint != "sentry" {
		t.Fatalf("round-tripped mapping = %+v", got)
	}
	if len(got.Fields) != 2 || got.Fields[0].Name != "title" || got.Fields[0].Path != "issue.title" ||
		got.Fields[0].Type != mapping.TypeString || !got.Fields[0].Required {
		t.Fatalf("round-tripped fields = %+v", got.Fields)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("CreatedAt/UpdatedAt must be stamped, got %+v", got)
	}
}

func TestGetMappingMissingReturnsNilNoError(t *testing.T) {
	s := openTest(t)
	got, err := s.GetMapping(t.Context(), 999)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got != nil {
		t.Fatalf("GetMapping for a missing id = %+v, want nil", got)
	}
}

func TestListMappingsOrdersNewestFirst(t *testing.T) {
	s := openTest(t)
	for i := range 3 {
		if _, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m", Fields: testFields()}); err != nil {
			t.Fatalf("InsertMapping[%d]: %v", i, err)
		}
	}
	got, err := s.ListMappings(t.Context())
	if err != nil {
		t.Fatalf("ListMappings: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListMappings = %d rows, want 3", len(got))
	}
	if got[0].ID <= got[1].ID || got[1].ID <= got[2].ID {
		t.Fatalf("ListMappings not newest-first: ids = %d, %d, %d", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestUpdateMappingChangesFieldsAndBumpsUpdatedAt(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "orig", Fields: testFields()})
	if err != nil {
		t.Fatalf("InsertMapping: %v", err)
	}
	before, err := s.GetMapping(t.Context(), id)
	if err != nil || before == nil {
		t.Fatalf("GetMapping before update: %+v, %v", before, err)
	}

	newFields := []mapping.Field{{Name: "renamed", Path: "a.b", Type: mapping.TypeBool}}
	if err := s.UpdateMapping(t.Context(), mapping.Mapping{
		ID: id, Name: "renamed mapping", SourceHint: "github", Fields: newFields,
	}); err != nil {
		t.Fatalf("UpdateMapping: %v", err)
	}

	after, err := s.GetMapping(t.Context(), id)
	if err != nil || after == nil {
		t.Fatalf("GetMapping after update: %+v, %v", after, err)
	}
	if after.Name != "renamed mapping" || after.SourceHint != "github" {
		t.Fatalf("updated mapping = %+v", after)
	}
	if len(after.Fields) != 1 || after.Fields[0].Name != "renamed" {
		t.Fatalf("updated fields = %+v", after.Fields)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) && !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("UpdatedAt did not advance: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestUpdateMappingMissingReturnsErrMappingNotFound(t *testing.T) {
	s := openTest(t)
	err := s.UpdateMapping(t.Context(), mapping.Mapping{ID: 999, Name: "x", Fields: testFields()})
	if !errors.Is(err, ErrMappingNotFound) {
		t.Fatalf("UpdateMapping on missing id = %v, want ErrMappingNotFound", err)
	}
}

func TestDeleteMappingRemovesRow(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertMapping(t.Context(), mapping.Mapping{Name: "m", Fields: testFields()})
	if err != nil {
		t.Fatalf("InsertMapping: %v", err)
	}
	if err := s.DeleteMapping(t.Context(), id); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}
	got, err := s.GetMapping(t.Context(), id)
	if err != nil {
		t.Fatalf("GetMapping after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("GetMapping after delete = %+v, want nil", got)
	}
}

func TestDeleteMappingMissingReturnsErrMappingNotFound(t *testing.T) {
	s := openTest(t)
	err := s.DeleteMapping(t.Context(), 999)
	if !errors.Is(err, ErrMappingNotFound) {
		t.Fatalf("DeleteMapping on missing id = %v, want ErrMappingNotFound", err)
	}
}
