package prbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/workflow/prreview/bench"
)

func TestProblemsAreTheRunnableSet(t *testing.T) {
	all, err := Problems(nil)
	if err != nil {
		t.Fatal(err)
	}
	goldens := 0
	seen := map[string]bool{}
	for _, p := range all {
		if seen[p.ID] || p.PRURL == "" || len(p.Goldens) == 0 {
			t.Errorf("problem %q: duplicate, or missing its PR or goldens", p.ID)
		}
		seen[p.ID] = true
		goldens += len(p.Goldens)
	}
	if len(all) != 38 || goldens != 102 {
		t.Errorf("got %d problems with %d goldens, want 38 with 102", len(all), goldens)
	}
	if _, err := Problems([]string{"nope#1"}); err == nil {
		t.Error("an unknown problem id was accepted")
	}
}

func TestFixtureReviewer(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
		want    int
	}{
		{name: "findings", body: `{"blast_radius_files": 12, "findings": [{"file": "a.go", "line_start": 3, "severity": "critical", "confidence": 0.8}]}`, want: 1},
		{name: "unknown severity", body: `{"findings": [{"file": "a.go", "severity": "High"}]}`, wantErr: "unknown severity"},
		{name: "missing file", wantErr: "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.body != "" {
				if err := os.WriteFile(filepath.Join(dir, "x#1.json"), []byte(tt.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := fixtureReviewer{dir: dir}.Review(t.Context(), bench.Problem{ID: "x#1"})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || len(got.Findings) != tt.want || got.Inputs.BlastRadiusFiles != 12 {
				t.Fatalf("Review = %+v, %v", got, err)
			}
		})
	}
}
