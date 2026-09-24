package archied

import (
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/bindingcipher"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// eventCaptureStore is every surface the State Store serves from the
// event-capture tables, plus the tool_call projection it writes through.
type eventCaptureStore interface {
	storecontract.CaptureStore
	storecontract.MappingStore
	storecontract.BindingStore
	storecontract.BindingDispatcher
	storecontract.PlaybookDispatcher
	toolCallWriter
}

// openEDAStore serves the event-capture store from the State Store's Postgres
// pool, sealing binding secrets with cipher.
func (b *boot) openEDAStore(cipher bindingcipher.BindingCipher) {
	b.eda = postgres.NewEDA(b.pg, cipher)
}
