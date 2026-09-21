package staterpc

import (
	"reflect"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/workflow/task"
)

// TestTaskProtoCarriesEveryDomainField fills every field of task.Task with a
// distinct non-zero value and requires the proto round trip to return it
// unchanged. taskProto/taskValue assign field by field, so a field added to
// the domain type and forgotten on the wire is otherwise invisible: the task
// still crosses, just with that field silently zeroed. This test fails for the
// forgotten field by name rather than leaving it to a caller to discover in
// production (archie-core-zf8h).
func TestTaskProtoCarriesEveryDomainField(t *testing.T) {
	stamp := time.Date(2026, 9, 21, 10, 30, 0, 0, time.UTC)

	want := &task.Task{}
	v := reflect.ValueOf(want).Elem()
	for i := range v.NumField() {
		field := v.Field(i)
		switch {
		case field.Type() == reflect.TypeFor[time.Time]():
			field.Set(reflect.ValueOf(stamp.Add(time.Duration(i) * time.Hour)))
		case field.Kind() == reflect.String:
			field.SetString(v.Type().Field(i).Name + "-value")
		case field.CanInt():
			field.SetInt(int64(i) + 1)
		default:
			t.Fatalf("field %s has kind %s, which this test does not know how to fill; extend it alongside the field", v.Type().Field(i).Name, field.Kind())
		}
	}

	got := taskValue(taskProto(want))
	if reflect.DeepEqual(got, want) {
		return
	}
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		gotField := reflect.ValueOf(got).Elem().Field(i).Interface()
		wantField := v.Field(i).Interface()
		if !reflect.DeepEqual(gotField, wantField) {
			t.Errorf("Task.%s did not survive the proto round trip: got %v, want %v", name, gotField, wantField)
		}
	}
}
