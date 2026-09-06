package archied

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type embeddedNATSEndpoint struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func embeddedNATSEndpointPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "nats", "endpoint.json")
}

func writeEmbeddedNATSEndpoint(dbPath, url, token string) error {
	path := embeddedNATSEndpointPath(dbPath)
	data, err := json.Marshal(embeddedNATSEndpoint{URL: url, Token: token})
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
		tmp.Close()
		return fmt.Errorf("protect embedded NATS endpoint: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
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

func readEmbeddedNATSEndpoint(dbPath string) (embeddedNATSEndpoint, error) {
	data, err := os.ReadFile(embeddedNATSEndpointPath(dbPath))
	if err != nil {
		return embeddedNATSEndpoint{}, err
	}
	var endpoint embeddedNATSEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return embeddedNATSEndpoint{}, fmt.Errorf("decode embedded NATS endpoint: %w", err)
	}
	if endpoint.URL == "" || endpoint.Token == "" {
		return embeddedNATSEndpoint{}, fmt.Errorf("embedded NATS endpoint is incomplete")
	}
	return endpoint, nil
}
