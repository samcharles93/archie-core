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
}

// Repository owns installed package records and dependency-safe deletion.
type Repository interface {
	Install(context.Context, Installed) error
	Get(context.Context, string, string) (Installed, error)
	List(context.Context, string) ([]Installed, error)
	Remove(context.Context, string, string) error
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
}

// Service validates the registry content before persisting an installation.
type Service struct {
	Registry Registry
	Store    Repository
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
	if err := s.Store.Install(ctx, installed); err != nil {
		return Installed{}, err
	}
	return installed, nil
}

func (s Service) Remove(ctx context.Context, orgID, name string) error {
	if strings.TrimSpace(orgID) == "" || strings.TrimSpace(name) == "" {
		return errors.New("org and name are required")
	}
	return s.Store.Remove(ctx, orgID, name)
}
