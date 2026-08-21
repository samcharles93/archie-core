package releaseupdate_test

import (
	"fmt"
	"sort"
	"testing"

	"github.com/samcharles93/archie-core/internal/releaseupdate"
)

const (
	daemon = releaseupdate.ComponentDaemon
	agent  = releaseupdate.ComponentAgent
)

// TestVerifyDetectsDrift covers the false-success bug this
// verification exists for: an installer reports a version it never actually
// managed to put into service, and nothing contradicts it because the health
// check only proves *a* daemon is answering -- possibly the old one.
func TestVerifyDetectsDrift(t *testing.T) {
	tests := []struct {
		name      string
		previous  map[string]string
		installed map[string]string
		running   map[string]string
		want      []releaseupdate.VersionDrift
	}{
		{
			name:      "claim matches running",
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
		{
			name:      "claim contradicted by the version it replaced",
			previous:  map[string]string{daemon: "1.9.8"},
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: "1.9.8"},
			want: []releaseupdate.VersionDrift{
				{ID: daemon, Claimed: "1.9.0", Running: "1.9.8", RunningIsPrevious: true},
			},
		},
		{
			name:      "running is neither installed nor previous",
			previous:  map[string]string{daemon: "1.9.8"},
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: "1.2.3"},
			want: []releaseupdate.VersionDrift{
				{ID: daemon, Claimed: "1.9.0", Running: "1.2.3", RunningIsPrevious: false},
			},
		},
		{
			name:      "unknown previous cannot make running the previous version",
			previous:  map[string]string{daemon: releaseupdate.VersionUnknown},
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: "1.9.8"},
			want: []releaseupdate.VersionDrift{
				{ID: daemon, Claimed: "1.9.0", Running: "1.9.8", RunningIsPrevious: false},
			},
		},
		{
			name:      "component with no running version is unverifiable, not wrong",
			installed: map[string]string{daemon: "1.9.0", agent: "1.9.0"},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
		{
			name:      "empty running version is unverifiable, not wrong",
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: ""},
			want:      nil,
		},
		{
			name:      "unknown running version is unverifiable, not wrong",
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: releaseupdate.VersionUnknown},
			want:      nil,
		},
		{
			name:      "unknown claim asserts nothing to contradict",
			installed: map[string]string{daemon: releaseupdate.VersionUnknown},
			running:   map[string]string{daemon: "1.9.8"},
			want:      nil,
		},
		{
			name:      "empty claim asserts nothing to contradict",
			installed: map[string]string{daemon: ""},
			running:   map[string]string{daemon: "1.9.8"},
			want:      nil,
		},
		{
			name:      "nothing running verifies nothing",
			installed: map[string]string{daemon: "1.9.0"},
			running:   nil,
			want:      nil,
		},
		{
			name:      "an unstamped running build is unverifiable, not wrong",
			installed: map[string]string{daemon: "1.9.0"},
			running:   map[string]string{daemon: releaseupdate.VersionDev},
			want:      nil,
		},
		{
			name:      "an unstamped claim asserts nothing to contradict",
			installed: map[string]string{daemon: releaseupdate.VersionDev},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
		{
			name:      "an upper-case tag prefix does not manufacture drift",
			installed: map[string]string{daemon: "V1.9.0"},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
		{
			name:      "a tag-shaped claim matches the stamped version it names",
			installed: map[string]string{daemon: "v1.9.0"},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
		{
			name:      "surrounding whitespace does not manufacture drift",
			installed: map[string]string{daemon: " 1.9.0\n"},
			running:   map[string]string{daemon: "1.9.0"},
			want:      nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := releaseupdate.Report{Previous: test.previous, Installed: test.installed}
			got := report.Verify(test.running).Drift
			if len(got) != len(test.want) {
				t.Fatalf("Verify().Drift = %#v, want %#v", got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Errorf("Verify().Drift[%d] = %#v, want %#v", i, got[i], test.want[i])
				}
			}
		})
	}
}

// TestVerifyPartitionsEveryClaim proves each claimed component lands in
// exactly one of Confirmed/Drift/Unverified. Callers word their message from
// which set is non-empty, so a component silently falling into none of them
// (or into two) is what would let an unchecked claim be reported as a
// checked one.
func TestVerifyPartitionsEveryClaim(t *testing.T) {
	tests := []struct {
		name           string
		claimed        string
		running        string
		wantConfirmed  bool
		wantDrift      bool
		wantUnverified bool
	}{
		{name: "agreeing versions confirm", claimed: "1.9.0", running: "1.9.0", wantConfirmed: true},
		{name: "disagreeing versions drift", claimed: "1.9.0", running: "1.9.8", wantDrift: true},
		{name: "unrecorded claim is unverified", claimed: releaseupdate.VersionUnknown, running: "1.9.8", wantUnverified: true},
		{name: "unstamped runner is unverified", claimed: "1.9.0", running: releaseupdate.VersionDev, wantUnverified: true},
		{name: "absent runner is unverified", claimed: "1.9.0", running: "", wantUnverified: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := releaseupdate.Report{Installed: map[string]string{daemon: test.claimed}}
			got := report.Verify(map[string]string{daemon: test.running})

			total := len(got.Confirmed) + len(got.Drift) + len(got.Unverified)
			if total != 1 {
				t.Fatalf("claim landed in %d sets, want exactly 1: %#v", total, got)
			}
			if (len(got.Confirmed) == 1) != test.wantConfirmed {
				t.Errorf("Confirmed = %v, want confirmed=%v", got.Confirmed, test.wantConfirmed)
			}
			if (len(got.Drift) == 1) != test.wantDrift {
				t.Errorf("Drift = %#v, want drift=%v", got.Drift, test.wantDrift)
			}
			if (len(got.Unverified) == 1) != test.wantUnverified {
				t.Errorf("Unverified = %v, want unverified=%v", got.Unverified, test.wantUnverified)
			}
		})
	}
}

// TestVerifyIgnoresUnclaimedRunningComponents proves a component
// that is running but was never part of the update is not treated as drift:
// the claim set is what is under test, not the inventory.
func TestVerifyIgnoresUnclaimedRunningComponents(t *testing.T) {
	report := releaseupdate.Report{Installed: map[string]string{daemon: "1.9.0"}}
	running := map[string]string{daemon: "1.9.0", agent: "0.0.1"}

	if got := report.Verify(running).Drift; len(got) != 0 {
		t.Fatalf("Verify().Drift = %#v, want no drift", got)
	}
}

// TestVerifyOrdersDriftStably guards the sort, which callers rely on
// to render a stable message.
//
// The component count is load-bearing and is NOT about permutations. Go
// randomizes a small map's iteration start offset rather than its order, so
// up to 8 keys in one bucket come out as a rotation -- an unsorted
// implementation still emits sorted output about 1/n of the time, not 1/n!.
// Measured against this code with the sort removed: 2 components pass 87% of
// the time and 8 pass 12%, while 9 crosses into bucket-order randomization
// and passes 0 times in 200,000. Nine is therefore the first count that
// fails an unsorted implementation on a single attempt; the repeats are
// belt-and-braces. Do not reduce either number.
func TestVerifyOrdersDriftStably(t *testing.T) {
	const components = 9
	installed := make(map[string]string, components)
	running := make(map[string]string, components)
	want := make([]string, 0, components)
	for i := range components {
		id := fmt.Sprintf("component-%d", i)
		installed[id] = "2.0.0"
		running[id] = "1.0.0"
		want = append(want, id)
	}
	sort.Strings(want)
	report := releaseupdate.Report{Installed: installed}

	for attempt := range 25 {
		drift := report.Verify(running).Drift
		if len(drift) != components {
			t.Fatalf("attempt %d: Verify().Drift returned %d drifts, want %d", attempt, len(drift), components)
		}
		for i, id := range want {
			if drift[i].ID != id {
				t.Fatalf("attempt %d: drift[%d].ID = %q, want %q (order is not stable)", attempt, i, drift[i].ID, id)
			}
		}
	}
}
