package secret

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const bwsTimeout = 30 * time.Second

type bwsEngine struct {
	once    sync.Once
	secrets map[string]string
	err     error
}

type bwsSecret struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func newBWSEngine() *bwsEngine { return &bwsEngine{} }

func (*bwsEngine) Name() string    { return "bws" }
func (*bwsEngine) Version() string { return "1.0.0" }

func (e *bwsEngine) Resolve(key string) (string, error) {
	e.once.Do(e.load)
	if e.err != nil {
		return "", e.err
	}
	value, ok := e.secrets[key]
	if !ok {
		return "", fmt.Errorf("bws secret %q not found", key)
	}
	return value, nil
}

func (e *bwsEngine) load() {
	command, err := findBWS()
	if err != nil {
		e.err = err
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), bwsTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, "secret", "list", "--output", "json")
	cmd.Env = os.Environ()
	cmd.Stderr = io.Discard
	raw, err := cmd.Output()
	if err != nil {
		e.err = fmt.Errorf("list bws secrets: %w", err)
		return
	}
	var records []bwsSecret
	if err := json.Unmarshal(raw, &records); err != nil {
		e.err = fmt.Errorf("decode bws secrets: %w", err)
		return
	}
	e.secrets = make(map[string]string, len(records))
	for _, record := range records {
		if record.Key != "" && record.Value != "" {
			e.secrets[record.Key] = record.Value
		}
	}
}

func findBWS() (string, error) {
	if command, err := exec.LookPath("bws"); err == nil {
		return command, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		command := filepath.Join(home, ".local", "bin", "bws")
		if info, statErr := os.Stat(command); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return command, nil
		}
	}
	return "", fmt.Errorf("bws executable not found")
}
