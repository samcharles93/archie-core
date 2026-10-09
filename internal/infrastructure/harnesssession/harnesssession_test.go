package harnesssession

import (
	"errors"
	"slices"
	"testing"
)

// A setup terminal must run a shell the image provides, not one archie assumes.
// bash is preferred when both exist; neither is the image's honest failure.
func TestPickShell(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		have    map[string]bool
		failing bool
		want    []string
		wantErr bool
	}{
		{name: "bash wins when both exist", have: map[string]bool{"bash": true, "sh": true}, want: []string{"bash", "-l"}},
		{name: "sh when bash is absent", have: map[string]bool{"sh": true}, want: []string{"sh", "-l"}},
		{name: "neither shell is an error", have: map[string]bool{}, wantErr: true},
		{name: "a probe that cannot run is not a shell", failing: true, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			probe := func(argv []string) (int, error) {
				if tc.failing {
					return 0, errors.New("exec failed")
				}
				if tc.have[argv[0]] {
					return 0, nil
				}
				return 127, nil
			}
			got, err := pickShell(probe)
			if tc.wantErr {
				if !errors.Is(err, ErrNoShell) {
					t.Fatalf("pickShell() = %v, want ErrNoShell", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("pickShell() = %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("pickShell() = %v, want %v", got, tc.want)
			}
		})
	}
}
