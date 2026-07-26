package secret

import (
	"fmt"
	"os"
)

// envEngine resolves secrets from environment variables. It is the
// default built-in engine and is always registered.
type envEngine struct{}

func (e *envEngine) Name() string    { return "env" }
func (e *envEngine) Version() string { return "1.0.0" }

func (e *envEngine) Resolve(key string) (string, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("secret: env var %q is not set", key)
	}
	return v, nil
}
