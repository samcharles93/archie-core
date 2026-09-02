package archied

import (
	"fmt"
	"strings"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/store"
)

// bindingCipherFromConfig resolves the binding at-rest encryption key from
// config through the secret registry and returns a store cipher. An unset
// encryption_key returns a nil cipher, keeping the legacy plaintext path (the
// store's default). See docs/prds/binding-secret-encryption.md.
func bindingCipherFromConfig(cfg config.Config, secrets *secret.Registry) (store.BindingCipher, error) {
	if cfg.Bindings.EncryptionKey == (secret.SecretRef{}) {
		return nil, nil
	}
	active, err := cfg.Bindings.EncryptionKey.Resolve(secrets)
	if err != nil {
		return nil, fmt.Errorf("resolve bindings encryption_key: %w", err)
	}
	active = strings.TrimSpace(active)
	if active == "" {
		return nil, fmt.Errorf("bindings encryption_key resolved empty")
	}

	previous := make([]string, 0, len(cfg.Bindings.PreviousEncryptionKeys))
	for _, ref := range cfg.Bindings.PreviousEncryptionKeys {
		v, err := ref.Resolve(secrets)
		if err != nil {
			return nil, fmt.Errorf("resolve bindings previous_encryption_key: %w", err)
		}
		if v = strings.TrimSpace(v); v != "" {
			previous = append(previous, v)
		}
	}
	return store.NewBindingCipher(active, previous)
}
