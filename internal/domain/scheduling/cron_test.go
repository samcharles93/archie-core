package scheduling

import (
	"errors"
	"slices"
	"testing"
)

func TestParseCronSegment(t *testing.T) {
	tests := []struct {
		segment string
		want    []int
		wantErr bool
	}{
		{segment: "*", want: []int{0, 1, 2, 3, 4, 5, 6}},
		{segment: "*/3", want: []int{0, 3, 6}},
		{segment: "1-5/2", want: []int{1, 3, 5}},
		{segment: "0,2-3,6", want: []int{0, 2, 3, 6}},
		{segment: "4", want: []int{4}},
		{segment: "4/2", wantErr: true},
		{segment: "5-2", wantErr: true},
		{segment: "*/7", wantErr: true},
		{segment: "1/2/3", wantErr: true},
		{segment: "1-2-3", wantErr: true},
		{segment: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.segment, func(t *testing.T) {
			got, err := parseCronSegment(tc.segment, 0, 6)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseCronSegment(%q) = %v, want an error", tc.segment, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCronSegment(%q): %v", tc.segment, err)
			}
			var slots []int
			for v := 0; v <= 6; v++ {
				if got.has(v) {
					slots = append(slots, v)
				}
			}
			if !slices.Equal(slots, tc.want) {
				t.Fatalf("parseCronSegment(%q) = %v, want %v", tc.segment, slots, tc.want)
			}
		})
	}
}

func TestParseCronSpecWrapsErrInvalidSpec(t *testing.T) {
	if _, err := parseCronSpec("0 0 * * 7"); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("parseCronSpec = %v, want ErrInvalidSpec", err)
	}
}
