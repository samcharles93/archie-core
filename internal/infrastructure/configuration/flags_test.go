package configuration

import "testing"

func TestSkipOverlayEnvSet(t *testing.T) {
	t.Setenv(envSkipOverlay, "1")
	if !SkipOverlay() {
		t.Fatal("SkipOverlay() = false with env set to 1")
	}
}

func TestSkipOverlayEnvUnset(t *testing.T) {
	t.Setenv(envSkipOverlay, "")
	if SkipOverlay() {
		t.Fatal("SkipOverlay() = true with env empty")
	}
}
