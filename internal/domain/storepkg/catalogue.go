package storepkg

import (
	"context"
	"errors"
	"fmt"
)

// ErrCatalogueUnavailable means no catalogue is configured for this instance.
var ErrCatalogueUnavailable = errors.New("no package catalogue is configured")

// CatalogueEntry is one package a catalogue offers: where it is and the
// digest that pins it.
type CatalogueEntry struct {
	Name        string `json:"name"`
	Surface     string `json:"surface"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Reference   string `json:"reference"`
	Digest      string `json:"digest"`
}

// Catalogue is a verified list of packages.
type Catalogue struct {
	Packages []CatalogueEntry `json:"packages"`
}

// Validate refuses an entry an install could not pin.
func (c Catalogue) Validate() error {
	seen := make(map[string]bool, len(c.Packages))
	for _, entry := range c.Packages {
		switch {
		case entry.Name == "" || entry.Reference == "":
			return fmt.Errorf("catalogue entry %q: name and reference are required", entry.Name)
		case !digestPattern.MatchString(entry.Digest):
			return fmt.Errorf("catalogue entry %q: digest must be a sha256 digest", entry.Name)
		case seen[entry.Name]:
			return fmt.Errorf("catalogue entry %q: listed twice", entry.Name)
		}
		seen[entry.Name] = true
	}
	return nil
}

// CatalogueSource fetches a catalogue and verifies its signature. An
// unverifiable catalogue is an error, never an empty list.
type CatalogueSource interface {
	Fetch(context.Context) (Catalogue, error)
}

// ListCatalogue returns the verified catalogue.
func (s Service) ListCatalogue(ctx context.Context) (Catalogue, error) {
	if s.Catalogue == nil {
		return Catalogue{}, ErrCatalogueUnavailable
	}
	return s.Catalogue.Fetch(ctx)
}

// InstallFromCatalogue installs name at the reference and digest the verified
// catalogue lists, so a package is never installed from an unverified tag.
func (s Service) InstallFromCatalogue(ctx context.Context, orgID, name string) (Installed, error) {
	catalogue, err := s.ListCatalogue(ctx)
	if err != nil {
		return Installed{}, err
	}
	for _, entry := range catalogue.Packages {
		if entry.Name == name {
			return s.Install(ctx, orgID, entry.Name, entry.Reference, entry.Digest)
		}
	}
	return Installed{}, fmt.Errorf("catalogue package %q: %w", name, ErrNotFound)
}
