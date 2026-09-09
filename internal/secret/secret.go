package secret

import "github.com/samcharles93/archie-core/internal/config"

// SecretRef moved to internal/config (archie-core-8cda.5.6): the type is
// config vocabulary, and config cannot import this package (the UI process
// renders config projections but never resolves a secret).
type SecretRef = config.SecretRef
