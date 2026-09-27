package prreview

import "testing"

func TestMergeDimensionsDropsDuplicatesByName(t *testing.T) {
	t.Parallel()

	lenses := [][]Dimension{
		{{Name: "security", Priority: 0.9}, {Name: "types", Priority: 0.4}},
		{{Name: "security", Priority: 0.5}, {Name: "docs", Priority: 0.2}},
	}
	got := MergeDimensions(lenses, 10)
	if len(got) != 3 {
		t.Fatalf("MergeDimensions() returned %d dimensions, want 3 (security, types, docs merged from duplicates)", len(got))
	}
	for _, d := range got {
		if d.Name == "security" && d.Priority != 0.9 {
			t.Errorf("duplicate %q kept priority %v, want the higher-priority lens's 0.9", d.Name, d.Priority)
		}
	}
}

func TestMergeDimensionsCapsAtTheDepthLimit(t *testing.T) {
	t.Parallel()

	lenses := [][]Dimension{
		{
			{Name: "a", Priority: 0.9},
			{Name: "b", Priority: 0.8},
			{Name: "c", Priority: 0.7},
			{Name: "d", Priority: 0.1},
		},
	}
	got := MergeDimensions(lenses, 2)
	if len(got) != 2 {
		t.Fatalf("MergeDimensions() with cap 2 returned %d dimensions, want 2", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("MergeDimensions() with cap 2 kept %+v, want the two highest-priority dimensions (a, b)", got)
	}
}

func TestMergeDimensionsIsDeterministicForTheSameInput(t *testing.T) {
	t.Parallel()

	lenses := [][]Dimension{{{Name: "x", Priority: 0.5}, {Name: "y", Priority: 0.5}}}

	got1 := MergeDimensions(lenses, 10)
	got2 := MergeDimensions(lenses, 10)
	if got1[0].Name != got2[0].Name || got1[1].Name != got2[1].Name {
		t.Errorf("MergeDimensions() on the same input produced different orders across calls: %+v vs %+v", got1, got2)
	}
}

func TestHallucinationDimensionOnlyAddedAboveTheAIGeneratedThreshold(t *testing.T) {
	t.Parallel()

	if got := HallucinationDimension(AIGeneratedThreshold); got != nil {
		t.Errorf("HallucinationDimension() at the threshold = %+v, want nil (the PRD says above, not at)", got)
	}
	got := HallucinationDimension(AIGeneratedThreshold + 0.01)
	if got == nil {
		t.Fatal("HallucinationDimension() above the threshold = nil, want a dimension")
	}
	if got.Name == "" || got.Prompt == "" {
		t.Errorf("HallucinationDimension() = %+v, want a named dimension with a reviewer prompt", got)
	}
}
