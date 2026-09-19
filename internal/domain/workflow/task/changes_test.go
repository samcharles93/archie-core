package task

import "testing"

// TestChangeStatusesAreSpelledAsPersisted pins the five status strings as
// literals. They are the on-disk vocabulary of a changes_captured payload. The
// dashboard gets their labels from the served catalogue
// (internal/webui/api_task_meta.go buildTaskMeta), pinned across the language
// boundary by internal/webui/testdata/task_meta.json and
// ui/test/task-meta-catalogue.test.js, so renaming one here fails that fixture
// pair rather than rendering raw status codes beside words.
func TestChangeStatusesAreSpelledAsPersisted(t *testing.T) {
	statuses := []struct {
		name string
		got  string
		want string
	}{
		{name: "ChangeAdded", got: ChangeAdded, want: "added"},
		{name: "ChangeModified", got: ChangeModified, want: "modified"},
		{name: "ChangeDeleted", got: ChangeDeleted, want: "deleted"},
		{name: "ChangeRenamed", got: ChangeRenamed, want: "renamed"},
		{name: "ChangeTypeChanged", got: ChangeTypeChanged, want: "typechange"},
	}

	for _, s := range statuses {
		if s.got != s.want {
			t.Errorf("%s = %q, want %q", s.name, s.got, s.want)
		}
	}
}
