package archied

import (
	"path/filepath"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

// openEDAStore opens the event-capture store (PocketBase today, Postgres when
// L2 ports it) beside the task database. It is a separate composition file
// from openTaskStore so L2 can replace edastore.Open here without colliding
// with L1's replacement of openProductionTaskStore -- the two lanes write
// different files.
func (b *boot) openEDAStore(cfg config.Config, cipher edastore.BindingCipher) error {
	path := taskDBPath(cfg.DBPath)
	eda, err := edastore.Open(edastore.Config{
		DBPath:  edaDBPath(cfg.DBPath),
		DataDir: filepath.Join(filepath.Dir(path), "eda"),
		Cipher:  cipher,
	})
	if err != nil {
		b.log.Error("open event-capture store", "err", err)
		return err
	}
	b.eda = eda
	b.addCleanup(func() {
		if err := eda.Close(); err != nil {
			b.log.Error("close event-capture store", "err", err)
		}
	})
	return nil
}
