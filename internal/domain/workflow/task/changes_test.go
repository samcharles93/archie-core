package task

import "testing"

// TestChangeStatusesAreSpelledAsPersisted pins the five status strings as
// literals. They are the on-disk vocabulary of a changes_captured payload and
// the dashboard mirrors them as its own key set
// (ui/src/tasks/changed-files.jsx FILE_STATUS), so renaming one here without
// the matching dashboard change renders raw status codes instead of words --
// with both suites still green unless each side pins the literal.
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
