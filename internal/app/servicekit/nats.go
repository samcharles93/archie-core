package servicekit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/config"
)

type NATSEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func embeddedNATSEndpointPath(stateDir string) string {
	return filepath.Join(stateDir, "nats", "endpoint.json")
}

func WriteNATSEndpoint(stateDir, url, token string) error {
	path := embeddedNATSEndpointPath(stateDir)
	data, err := json.Marshal(NATSEndpoint{URL: url, Token: token})
	if err != nil {
		return fmt.Errorf("marshal embedded NATS endpoint: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create embedded NATS directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".endpoint-*")
	if err != nil {
		return fmt.Errorf("create embedded NATS endpoint: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect embedded NATS endpoint: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write embedded NATS endpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close embedded NATS endpoint: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("publish embedded NATS endpoint: %w", err)
	}
	return nil
}

func ReadNATSEndpoint(stateDir string) (NATSEndpoint, error) {
	data, err := os.ReadFile(embeddedNATSEndpointPath(stateDir))
	if err != nil {
		return NATSEndpoint{}, err
	}
	var endpoint NATSEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return NATSEndpoint{}, fmt.Errorf("decode embedded NATS endpoint: %w", err)
	}
	if endpoint.URL == "" || endpoint.Token == "" {
		return NATSEndpoint{}, fmt.Errorf("embedded NATS endpoint is incomplete")
	}
	return endpoint, nil
}

func NATSToken(cfg config.NATSConfig, getenv func(string) string) (string, error) {
	if cfg.TokenEnv == "" {
		return "", nil
	}
	token := getenv(cfg.TokenEnv)
	if token == "" {
		return "", fmt.Errorf("%s is required when nats.token_env is configured", cfg.TokenEnv)
	}
	return token, nil
}
