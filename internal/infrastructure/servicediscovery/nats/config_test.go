package nats

import (
	"errors"
	"testing"
	"time"
)

func TestWithDefaults(t *testing.T) {
	cfg := (Config{}).withDefaults()
	if cfg.Bucket != DefaultBucket {
		t.Errorf("withDefaults().Bucket = %q, want %q", cfg.Bucket, DefaultBucket)
	}
	if cfg.InstalledBucket != DefaultInstalledBucket {
		t.Errorf("withDefaults().InstalledBucket = %q, want %q", cfg.InstalledBucket, DefaultInstalledBucket)
	}
	if cfg.TTL != DefaultTTL {
		t.Errorf("withDefaults().TTL = %v, want %v", cfg.TTL, DefaultTTL)
	}
	if cfg.Heartbeat != DefaultHeartbeat {
		t.Errorf("withDefaults().Heartbeat = %v, want %v", cfg.Heartbeat, DefaultHeartbeat)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "valid", cfg: Config{URL: "nats://x", TTL: 15 * time.Second, Heartbeat: 5 * time.Second}, wantErr: false},
		{name: "missing url", cfg: Config{TTL: time.Second, Heartbeat: 200 * time.Millisecond}, wantErr: true},
		{name: "heartbeat equals ttl", cfg: Config{URL: "nats://x", TTL: time.Second, Heartbeat: time.Second}, wantErr: true},
		{name: "heartbeat exceeds ttl", cfg: Config{URL: "nats://x", TTL: time.Second, Heartbeat: 2 * time.Second}, wantErr: true},
		{name: "negative durations", cfg: Config{URL: "nats://x", TTL: -time.Second, Heartbeat: time.Second}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr {
				if !errors.Is(err, ErrInvalidConfig) {
					t.Fatalf("Validate() = %v, want ErrInvalidConfig", err)
				}
			} else if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}
