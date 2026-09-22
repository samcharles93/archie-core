package configuration

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestValidateCurators(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *config.Config)
		wantErr bool
	}{
		{
			name:   "no curators",
			mutate: func(cfg *config.Config) {},
		},
		{
			name: "valid curator",
			mutate: func(cfg *config.Config) {
				cfg.Curators = []config.CuratorDefinition{{Name: "project", Enabled: true, Interval: config.Duration(time.Hour)}}
			},
		},
		{
			name: "missing interval",
			mutate: func(cfg *config.Config) {
				cfg.Curators = []config.CuratorDefinition{{Name: "project", Enabled: true}}
			},
			wantErr: true,
		},
		{
			name: "zero interval",
			mutate: func(cfg *config.Config) {
				cfg.Curators = []config.CuratorDefinition{{Name: "project", Enabled: true, Interval: config.Duration(0)}}
			},
			wantErr: true,
		},
		{
			name: "negative interval",
			mutate: func(cfg *config.Config) {
				cfg.Curators = []config.CuratorDefinition{{Name: "project", Enabled: true, Interval: config.Duration(-time.Minute)}}
			},
			wantErr: true,
		},
		{
			// A definition must be valid seed data whether or not it is
			// currently enabled: flipping Enabled on later must not silently
			// default the interval.
			name: "disabled curator still requires interval",
			mutate: func(cfg *config.Config) {
				cfg.Curators = []config.CuratorDefinition{{Name: "project"}}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			tt.mutate(&cfg)
			err := Validate(&cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
