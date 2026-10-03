package secret

import "github.com/samcharles93/archie-core/internal/config"

// SecretRef moved to internal/config: the type is
// config vocabulary, and config cannot import this package (the UI process
// renders config projections but never resolves a secret).
type SecretRef = config.SecretRef
