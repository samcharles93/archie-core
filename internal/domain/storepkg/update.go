package storepkg

import (
	"context"
	"errors"
	"fmt"
)

// Update policies. Manual is the default; an auto package follows the
// catalogue, with a widening update still waiting for approval.
const (
	PolicyManual = "manual"
	PolicyAuto   = "auto"
)

// ErrInvalidPolicy: the update policy is neither manual nor auto.
var ErrInvalidPolicy = errors.New("store package update policy must be manual or auto")

// OrgPackage names one installed package.
type OrgPackage struct{ OrgID, Name string }

// SetPackageUpdatePolicy sets whether name follows the catalogue on its own.
func (s Service) SetPackageUpdatePolicy(ctx context.Context, orgID, name, policy string) (Installed, error) {
	if policy != PolicyManual && policy != PolicyAuto {
		return Installed{}, ErrInvalidPolicy
	}
	if err := s.Store.SetUpdatePolicy(ctx, orgID, name, policy); err != nil {
		return Installed{}, err
	}
	return s.Store.Get(ctx, orgID, name)
}

// UpdateAuto runs UpdatePackage for every auto package.
func (s Service) UpdateAuto(ctx context.Context) error {
	return updateAuto(ctx, s.Store, s)
}

// updateAuto tries every auto package: one that fails does not stop the rest.
func updateAuto(ctx context.Context, store interface {
	ListAutoUpdate(context.Context) ([]OrgPackage, error)
}, updates OrgUpdates,
) error {
	refs, err := store.ListAutoUpdate(ctx)
	if err != nil {
		return fmt.Errorf("list auto-update packages: %w", err)
	}
	var errs []error
	for _, ref := range refs {
		if _, err := updates.UpdatePackage(ctx, ref.OrgID, ref.Name); err != nil {
			errs = append(errs, fmt.Errorf("update %s/%s: %w", ref.OrgID, ref.Name, err))
		}
	}
	return errors.Join(errs...)
}

var (
	// ErrNoPendingUpdate: there is no update waiting for approval.
	ErrNoPendingUpdate = errors.New("store package has no pending update")
	// ErrNoPrevious: the package has no earlier version to roll back to.
	ErrNoPrevious = errors.New("store package has no previous version")
)

// Widens reports whether an update's declared authority asks for more than
// the operator accepted. A package never accepted holds no authority, so
// any grant it declares is wider.
func (i Installed) Widens(next Authority) bool {
	var held Authority
	if i.AcceptedAuthority != nil {
		held = *i.AcceptedAuthority
	}
	return !held.Covers(next)
}

// UpdatePackage moves name to the digest the verified catalogue lists. An
// update that does not widen authority applies at once; a widening one waits
// as pending until ApprovePackageUpdate, with the live pin untouched.
func (s Service) UpdatePackage(ctx context.Context, orgID, name string) (Installed, error) {
	installed, err := s.Store.Get(ctx, orgID, name)
	if err != nil {
		return Installed{}, fmt.Errorf("installed package %q: %w", name, err)
	}
	catalogue, err := s.ListCatalogue(ctx)
	if err != nil {
		return Installed{}, err
	}
	for _, entry := range catalogue.Packages {
		if entry.Name != name {
			continue
		}
		pin := Pin{Reference: entry.Reference, Digest: entry.Digest}
		if pin.Digest == installed.Digest || (installed.Pending != nil && installed.Pending.Digest == pin.Digest) {
			return installed, nil
		}
		next, contents, err := s.prepare(ctx, orgID, name, pin)
		if err != nil {
			return Installed{}, err
		}
		if installed.Widens(next.Descriptor.Authority) {
			if err := s.Store.SetPending(ctx, orgID, name, pin, next.Descriptor.Authority); err != nil {
				return Installed{}, err
			}
			return s.Store.Get(ctx, orgID, name)
		}
		next.AcceptedAuthority = installed.AcceptedAuthority
		return s.swap(ctx, installed, next, contents)
	}
	return Installed{}, fmt.Errorf("catalogue package %q: %w", name, ErrNotFound)
}

// ApprovePackageUpdate applies the pending update and accepts the authority
// it declares.
func (s Service) ApprovePackageUpdate(ctx context.Context, orgID, name string) (Installed, error) {
	installed, err := s.Store.Get(ctx, orgID, name)
	if err != nil {
		return Installed{}, fmt.Errorf("installed package %q: %w", name, err)
	}
	if installed.Pending == nil {
		return Installed{}, fmt.Errorf("package %q: %w", name, ErrNoPendingUpdate)
	}
	next, contents, err := s.prepare(ctx, orgID, name, *installed.Pending)
	if err != nil {
		return Installed{}, err
	}
	accepted := next.Descriptor.Authority
	next.AcceptedAuthority = &accepted
	return s.swap(ctx, installed, next, contents)
}

// RollbackPackage reinstalls the previous digest with the authority accepted
// against it.
func (s Service) RollbackPackage(ctx context.Context, orgID, name string) (Installed, error) {
	installed, err := s.Store.Get(ctx, orgID, name)
	if err != nil {
		return Installed{}, fmt.Errorf("installed package %q: %w", name, err)
	}
	if installed.Previous == nil {
		return Installed{}, fmt.Errorf("package %q: %w", name, ErrNoPrevious)
	}
	next, contents, err := s.prepare(ctx, orgID, name, *installed.Previous)
	if err != nil {
		return Installed{}, err
	}
	next.AcceptedAuthority = installed.PreviousAccepted
	return s.swap(ctx, installed, next, contents)
}

// prepare fetches and validates the package at pin and resolves its
// contributions before anything live changes.
func (s Service) prepare(ctx context.Context, orgID, name string, pin Pin) (Installed, map[string]map[string][]byte, error) {
	descriptor, layer, err := s.Registry.Fetch(ctx, pin.Reference, pin.Digest)
	if err != nil {
		return Installed{}, nil, fmt.Errorf("fetch package: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return Installed{}, nil, fmt.Errorf("validate package: %w", err)
	}
	contents, err := s.resolveFamilyContents(descriptor, layer)
	if err != nil {
		return Installed{}, nil, err
	}
	return Installed{
		OrgID: orgID, Name: name, Reference: pin.Reference, Digest: pin.Digest,
		Descriptor: descriptor, Layer: layer,
	}, contents, nil
}

// swap replaces installed with next: old contributions come out, new ones go
// in, then the record flips. A failure puts the old contributions back so
// the record, which still names the old pin, stays true.
func (s Service) swap(ctx context.Context, installed, next Installed, contents map[string]map[string][]byte) (Installed, error) {
	if err := s.withdraw(ctx, installed); err != nil {
		return Installed{}, err
	}
	err := s.project(ctx, next, contents)
	if err == nil {
		err = s.Store.Replace(ctx, next)
	}
	if err != nil {
		return Installed{}, errors.Join(err, s.withdraw(ctx, next), s.restore(ctx, installed))
	}
	return s.Store.Get(ctx, installed.OrgID, installed.Name)
}

func (s Service) restore(ctx context.Context, installed Installed) error {
	contents, err := s.resolveFamilyContents(installed.Descriptor, installed.Layer)
	if err != nil {
		return err
	}
	return s.project(ctx, installed, contents)
}
