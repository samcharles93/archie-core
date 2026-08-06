package curator

import (
	"testing"
	"time"
)

func TestManifestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest Manifest
		wantErr  bool
	}{
		{
			name:     "valid minimal",
			manifest: Manifest{Interval: time.Hour},
		},
		{
			name: "valid full",
			manifest: Manifest{
				Interval:     time.Hour,
				Cooldown:     time.Minute,
				OnInput:      true,
				Tools:        []string{"skills.list", "skills.write"},
				Skills:       true,
				MemoryEngine: "builtin",
				Model:        "provider/model",
			},
		},
		{
			name:     "zero interval",
			manifest: Manifest{},
			wantErr:  true,
		},
		{
			name:     "negative interval",
			manifest: Manifest{Interval: -time.Hour},
			wantErr:  true,
		},
		{
			name:     "empty tool name",
			manifest: Manifest{Interval: time.Hour, Tools: []string{""}},
			wantErr:  true,
		},
		{
			name:     "blank tool name",
			manifest: Manifest{Interval: time.Hour, Tools: []string{"  "}},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.manifest.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManifestValidateRejectsDuplicateTools(t *testing.T) {
	t.Parallel()

	m := Manifest{Interval: time.Hour, Tools: []string{"skills.list", "skills.list"}}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil (duplicates are the builder's problem)", err)
	}
}
