package installtype

import "testing"

func TestTypeDefaultsToUnknown(t *testing.T) {
	if got := Type(); got != Unknown {
		t.Errorf("Type() = %q, want %q (a go test binary is never ldflags-stamped)", got, Unknown)
	}
}

// TestTypeReflectsAStampedBuildType proves Type reads the exact variable
// "-ldflags -X .../installtype.buildType=<value>" sets. go test cannot pass
// -ldflags to the package under test, so this is the closest a unit test can
// get to exercising the release pipeline's stamping without actually
// building a release artifact.
func TestTypeReflectsAStampedBuildType(t *testing.T) {
	tests := []struct {
		name  string
		stamp string
		want  string
	}{
		{"container stamp", "container", "container"},
		{"binary stamp", "binary", "binary"},
		{"empty stamp is treated as unknown", "", Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := buildType
			t.Cleanup(func() { buildType = original })

			buildType = tt.stamp
			if got := Type(); got != tt.want {
				t.Errorf("Type() = %q, want %q", got, tt.want)
			}
		})
	}
}
