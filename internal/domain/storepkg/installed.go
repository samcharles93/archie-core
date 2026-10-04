package storepkg

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound  = errors.New("store package not found")
	ErrInstalled = errors.New("store package already installed")
	ErrRequired  = errors.New("store package is required by another installed package")
	// ErrAuthorityNotDeclared: the operator asked to accept authority the
	// package itself does not declare, which is not an acceptance of that
	// package. Acceptance records the package's own declared grants against
	// the pinned digest.
	ErrAuthorityNotDeclared = errors.New("store package does not declare this authority")
	// ErrContributionCollision: the package contributes an entry the org
	// resource already holds under an id the package never projected. The
	// contribution is refused rather than replacing the operator's entry.
	ErrContributionCollision = errors.New("package contribution replaces an entry it does not own")
)

// Installed is one organisation's pinned Archie package.
type Installed struct {
	OrgID        string
	Name         string
	Reference    string
	Digest       string
	Descriptor   Descriptor
	Layer        []byte
	UpdatePolicy string
	// AcceptedAuthority is the operator's acceptance, recorded against the
	// pinned digest. A nil Authority means the operator has not accepted;
	// until then the package's authority at use time is nothing.
	AcceptedAuthority *Authority
}

// Repository owns installed package records and dependency-safe deletion.
type Repository interface {
	Install(context.Context, Installed) error
	Get(context.Context, string, string) (Installed, error)
	List(context.Context, string) ([]Installed, error)
	Remove(context.Context, string, string) error
	// Accept records the operator's accepted authority against the package's
	// currently pinned digest.
	Accept(context.Context, string, string, Authority) error
}

// Registry reads a package by immutable OCI manifest digest.
type Registry interface {
	Fetch(context.Context, string, string) (Descriptor, []byte, error)
}

// Manager is the State Store surface for installed packages.
type Manager interface {
	InstallPackage(context.Context, string, string, string, string) (Installed, error)
	GetInstalled(context.Context, string, string) (Installed, error)
	ListInstalled(context.Context, string) ([]Installed, error)
	RemoveInstalled(context.Context, string, string) error
	AcceptPackageAuthority(ctx context.Context, orgID, name string, accepted Authority) (Installed, error)
}

// Installations is Manager as a remote caller sees it: the State Store acts
// in the caller's org, so no method names one.
type Installations interface {
	InstallPackage(ctx context.Context, name, reference, digest string) (Installed, error)
	GetInstalled(ctx context.Context, name string) (Installed, error)
	ListInstalled(ctx context.Context) ([]Installed, error)
	RemoveInstalled(ctx context.Context, name string) error
	AcceptPackageAuthority(ctx context.Context, name string, accepted Authority) (Installed, error)
}

// Service validates the registry content before persisting an installation.
type Service struct {
	Registry Registry
	Store    Repository
	// Projections carries, per descriptor family, the projector that applies
	// a package's contribution to the org resource that family projects to,
	// and withdraws it again on removal. A family with no entry is stored but
	// never projected: installing leaves it unused until a projector lands.
	Projections map[string]FamilyProjector
}

var _ Manager = Service{}

func (s Service) InstallPackage(ctx context.Context, orgID, name, reference, digest string) (Installed, error) {
	return s.Install(ctx, orgID, name, reference, digest)
}

func (s Service) GetInstalled(ctx context.Context, orgID, name string) (Installed, error) {
	return s.Store.Get(ctx, orgID, name)
}

func (s Service) ListInstalled(ctx context.Context, orgID string) ([]Installed, error) {
	return s.Store.List(ctx, orgID)
}

func (s Service) RemoveInstalled(ctx context.Context, orgID, name string) error {
	return s.Remove(ctx, orgID, name)
}

// AcceptPackageAuthority records the operator's acceptance against the pinned
// digest. Only grants the package declares are acceptable, and the accepted
// record may be narrower than the declaration: the record, not the descriptor,
// is the authority the package is later checked against.
func (s Service) AcceptPackageAuthority(ctx context.Context, orgID, name string, accepted Authority) (Installed, error) {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(name) == "" {
		return Installed{}, errors.New("org and name are required")
	}
	if err := accepted.Validate(); err != nil {
		return Installed{}, fmt.Errorf("accepted authority: %w", err)
	}
	installed, err := s.Store.Get(ctx, orgID, name)
	if err != nil {
		return Installed{}, fmt.Errorf("installed package %q: %w", name, err)
	}
	if !installed.Descriptor.Authority.Covers(accepted) {
		return Installed{}, fmt.Errorf("package %q: %w", name, ErrAuthorityNotDeclared)
	}
	if err := s.Store.Accept(ctx, orgID, name, accepted); err != nil {
		return Installed{}, err
	}
	return s.Store.Get(ctx, orgID, name)
}

func (s Service) Install(ctx context.Context, orgID, name, reference, digest string) (Installed, error) {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(reference) == "" {
		return Installed{}, errors.New("org, name and reference are required")
	}
	if !digestPattern.MatchString(digest) {
		return Installed{}, errors.New("package digest must be a sha256 digest")
	}
	descriptor, layer, err := s.Registry.Fetch(ctx, reference, digest)
	if err != nil {
		return Installed{}, fmt.Errorf("fetch package: %w", err)
	}
	if err := descriptor.Validate(); err != nil {
		return Installed{}, fmt.Errorf("validate package: %w", err)
	}
	for _, required := range descriptor.Requires {
		dependency, err := s.Store.Get(ctx, orgID, required.Name)
		if err != nil {
			return Installed{}, fmt.Errorf("required package %q: %w", required.Name, err)
		}
		if dependency.Digest != required.Digest {
			return Installed{}, fmt.Errorf("required package %q has a different digest", required.Name)
		}
	}
	installed := Installed{
		OrgID: orgID, Name: name, Reference: reference, Digest: digest,
		Descriptor: descriptor, Layer: layer, UpdatePolicy: "manual",
	}
	contents, err := s.resolveFamilyContents(descriptor, layer)
	if err != nil {
		return Installed{}, err
	}
	if err := s.Store.Install(ctx, installed); err != nil {
		return Installed{}, err
	}
	if err := s.project(ctx, installed, contents); err != nil {
		// An install without its projected contributions is not usable. Take
		// whatever was projected back out, then the installation itself, so the
		// org is left with no package at all rather than a half-usable one.
		return Installed{}, errors.Join(err, s.withdraw(ctx, installed), s.Store.Remove(ctx, orgID, name))
	}
	return installed, nil
}

func (s Service) Remove(ctx context.Context, orgID, name string) error {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(name) == "" {
		return errors.New("org and name are required")
	}
	installed, err := s.Store.Get(ctx, orgID, name)
	if err != nil {
		return fmt.Errorf("installed package %q: %w", name, err)
	}
	// Withdraw before the record goes: the package's contributions must stop
	// being usable only once its installation stops being true. A withdrawal
	// that fails keeps the package installed and its record kept.
	if err := s.withdraw(ctx, installed); err != nil {
		return err
	}
	return s.Store.Remove(ctx, orgID, name)
}
