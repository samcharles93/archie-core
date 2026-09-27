package prreview

import "testing"

func TestClassifyDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		changedLines int
		want         Depth
	}{
		{"zero lines is quick", 0, DepthQuick},
		{"just under the quick boundary", 99, DepthQuick},
		{"the quick boundary itself is standard", 100, DepthStandard},
		{"just under the standard boundary", 499, DepthStandard},
		{"the standard boundary itself is deep", 500, DepthDeep},
		{"far above the standard boundary is deep", 5000, DepthDeep},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyDepth(tt.changedLines); got != tt.want {
				t.Errorf("ClassifyDepth(%d) = %q, want %q", tt.changedLines, got, tt.want)
			}
		})
	}
}

func TestMaxDimensionsFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		depth Depth
		want  int
	}{
		{DepthQuick, 3},
		{DepthStandard, 6},
		{DepthDeep, 12},
		{Depth("unknown"), 3}, // an unrecognised depth is treated as the cheapest, never the most expensive
	}
	for _, tt := range tests {
		if got := MaxDimensionsFor(tt.depth); got != tt.want {
			t.Errorf("MaxDimensionsFor(%q) = %d, want %d", tt.depth, got, tt.want)
		}
	}
}

func TestExplicitDepthOverridesClassification(t *testing.T) {
	t.Parallel()

	got := ResolveDepth(5000, DepthQuick)
	if got != DepthQuick {
		t.Errorf("ResolveDepth with an explicit operator depth = %q, want %q (the explicit value must win over the line count)", got, DepthQuick)
	}

	got = ResolveDepth(5000, "")
	if got != DepthDeep {
		t.Errorf("ResolveDepth with no explicit depth = %q, want %q (falls back to classification)", got, DepthDeep)
	}
}
