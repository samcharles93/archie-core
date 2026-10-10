package archied

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// installedAuthorities answers a package's accepted authority from the State
// Store. A package the operator has not accepted has none.
type installedAuthorities struct {
	packages storepkg.Installations
}

func (a installedAuthorities) AcceptedAuthority(ctx context.Context, name string) (storepkg.Authority, error) {
	installed, err := a.packages.GetInstalled(ctx, name)
	if err != nil || installed.AcceptedAuthority == nil {
		return storepkg.Authority{}, err
	}
	return *installed.AcceptedAuthority, nil
}
