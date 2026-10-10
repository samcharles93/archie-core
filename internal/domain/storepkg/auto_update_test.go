package storepkg

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type fakeAuto struct {
	refs    []OrgPackage
	listErr error
	failing string
	tried   []string
}

func (f *fakeAuto) ListAutoUpdate(context.Context) ([]OrgPackage, error) { return f.refs, f.listErr }

func (f *fakeAuto) UpdatePackage(_ context.Context, _, name string) (Installed, error) {
	f.tried = append(f.tried, name)
	if name == f.failing {
		return Installed{}, errors.New("catalogue unreachable")
	}
	return Installed{}, nil
}

func (f *fakeAuto) ApprovePackageUpdate(context.Context, string, string) (Installed, error) {
	return Installed{}, nil
}

func (f *fakeAuto) RollbackPackage(context.Context, string, string) (Installed, error) {
	return Installed{}, nil
}

func (f *fakeAuto) SetPackageUpdatePolicy(context.Context, string, string, string) (Installed, error) {
	return Installed{}, nil
}
func (f *fakeAuto) UpdateAuto(context.Context) error { return nil }

// A tick updates every package the store lists as auto, and one failing
// package neither stops the rest nor hides its error.
func TestUpdateAuto(t *testing.T) {
	tests := []struct {
		name    string
		fake    fakeAuto
		tried   []string
		wantErr bool
	}{
		{name: "no auto packages", tried: nil},
		{name: "every auto package is tried", fake: fakeAuto{refs: []OrgPackage{{"o", "a"}, {"o", "b"}}}, tried: []string{"a", "b"}},
		{name: "a failure does not stop the rest", fake: fakeAuto{refs: []OrgPackage{{"o", "a"}, {"o", "b"}}, failing: "a"}, tried: []string{"a", "b"}, wantErr: true},
		{name: "a failed listing is an error", fake: fakeAuto{listErr: errors.New("db down")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := updateAuto(t.Context(), &tt.fake, &tt.fake)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tt.wantErr)
			}
			if !slices.Equal(tt.fake.tried, tt.tried) {
				t.Fatalf("tried %v; want %v", tt.fake.tried, tt.tried)
			}
		})
	}
}
