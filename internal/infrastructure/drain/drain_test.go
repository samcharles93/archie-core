package drain

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/drain"
)

func TestParseProcStatField22(t *testing.T) {
	tests := []struct {
		name    string
		stat    string
		want    int64
		wantErr bool
	}{
		{
			name: "systemd pid 1 with real start time",
			stat: "1 (systemd) S 0 1 1 0 -1 4194560 214130 42927786 23 588 817 557 98753 15714 20 0 1 0 5 0 0 0 0 ...",
			want: 5,
		},
		{
			name: "comm containing spaces is handled",
			stat: "1 (init daemon) S 0 1 1 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 42 0 0 0 0 ...",
			want: 42,
		},
		{
			name: "many trailing fields (real proc stat has 50+) are tolerated",
			stat: "1 (systemd) S 0 1 1 0 -1 4194560 214130 42927786 23 588 817 557 98753 15714 20 0 1 0 9 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0",
			want: 9,
		},
		{
			name:    "too few post-paren fields fails",
			stat:    "1 (systemd) S 0 1 1",
			wantErr: true,
		},
		{
			name:    "missing closing paren fails",
			stat:    "1 systemd S 0 1 1",
			wantErr: true,
		},
		{
			name:    "non-numeric start time fails",
			stat:    "1 (systemd) S 0 1 1 0 -1 4194560 214130 42927786 23 588 817 557 98753 15714 20 0 1 0 notanumber 0 0 0 0",
			wantErr: true,
		},
		{
			name:    "zero start time fails",
			stat:    "1 (systemd) S 0 1 1 0 -1 4194560 214130 42927786 23 588 817 557 98753 15714 20 0 1 0 0 0 0 0 0",
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseProcStat(test.stat)
			if (err != nil) != test.wantErr {
				t.Fatalf("parseProcStat() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("parseProcStat() = %d, want %d", got, test.want)
			}
		})
	}
}

// writeMarker writes a drain marker JSON file with the given epoch.
func writeMarker(t *testing.T, dir string, epoch drain.Epoch) string {
	t.Helper()
	path := filepath.Join(dir, DefaultMarkerFilename)
	payload, err := json.Marshal(drain.Marker{InstantiationEpoch: epoch, Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReaderCheckTable(t *testing.T) {
	current := drain.Epoch{BootID: "boot-now", PID1Start: 100}
	prior := drain.Epoch{BootID: "boot-old", PID1Start: 100}

	match := func() (drain.Epoch, error) { return current, nil }

	tests := []struct {
		name    string
		write   func(dir string) string
		epoch   func() (drain.Epoch, error)
		want    drain.Decision
		wantErr bool
	}{
		{
			name: "absent marker reports none",
			write: func(dir string) string {
				return filepath.Join(dir, DefaultMarkerFilename)
			},
			epoch: match,
			want:  drain.DecisionNone,
		},
		{
			name:  "marker with current epoch is valid",
			write: func(dir string) string { return writeMarker(t, dir, current) },
			epoch: match,
			want:  drain.DecisionValid,
		},
		{
			name:  "marker written against a prior epoch is stale",
			write: func(dir string) string { return writeMarker(t, dir, prior) },
			epoch: match,
			want:  drain.DecisionStale,
		},
		{
			name:  "current epoch unreadable fails closed to stale",
			write: func(dir string) string { return writeMarker(t, dir, current) },
			epoch: func() (drain.Epoch, error) {
				return drain.Epoch{}, errors.New("proc unavailable")
			},
			want:    drain.DecisionStale,
			wantErr: true,
		},
		{
			name: "malformed marker fails closed to stale",
			write: func(dir string) string {
				path := filepath.Join(dir, DefaultMarkerFilename)
				if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			epoch:   match,
			want:    drain.DecisionStale,
			wantErr: true,
		},
		{
			name: "marker file is a directory fails closed to stale",
			write: func(dir string) string {
				path := filepath.Join(dir, DefaultMarkerFilename)
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
			epoch:   match,
			want:    drain.DecisionStale,
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := test.write(dir)
			reader := New(path, test.epoch)
			got, err := reader.Check()
			if (err != nil) != test.wantErr {
				t.Fatalf("Check() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("Check() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReaderParsesOperatorWrittenJSON(t *testing.T) {
	// The marker is written by an external operator script, so the on-disk
	// shape (underscore keys) is part of the contract. Unmarshal it as-is and
	// confirm the epoch round-trips to a valid decision -- this is what guards
	// the JSON tag wiring rather than go fixture marshaling.
	current := drain.Epoch{BootID: "boot-now", PID1Start: 100}
	const raw = `{"instantiation_epoch":{"boot_id":"boot-now","pid1_start":100},"reason":"maintenance window","requested_at":"2026-09-05T00:00:00Z"}`

	dir := t.TempDir()
	path := filepath.Join(dir, DefaultMarkerFilename)
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := New(path, func() (drain.Epoch, error) { return current, nil }).Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got != drain.DecisionValid {
		t.Fatalf("Check() = %v, want valid", got)
	}
}

func TestNewDefaultsToCurrentEpoch(t *testing.T) {
	dir := t.TempDir()
	// No epoch function passed: New must fall back to the package's real
	// CurrentEpoch. The marker records the real current epoch read from /proc,
	// so Check must report it valid -- proving the default wiring works end to
	// end without a host restart.
	current, err := CurrentEpoch()
	if err != nil {
		t.Skipf("cannot read current epoch on this host: %v", err)
	}
	path := writeMarker(t, dir, current)
	got, err := New(path, nil).Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got != drain.DecisionValid {
		t.Fatalf("Check() = %v, want valid", got)
	}
}
