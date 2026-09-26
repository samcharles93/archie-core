package postgres

import (
	"errors"
	"testing"

	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

func TestInstalledPackagesOrgPinsAndDependencies(t *testing.T) {
	pool, _ := migrated(t)
	s := NewInstalledPackages(pool)
	ctx := t.Context()
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	base := storepkg.Descriptor{
		APIVersion: storepkg.APIVersion, DisplayName: "Kit", Version: "1",
		Contributes: storepkg.Contributions{Skills: []string{"skill"}},
	}
	kit := storepkg.Installed{OrgID: "org-a", Name: "kit", Reference: "localhost:5000/kit", Digest: digest, Descriptor: base, Layer: []byte("kit"), UpdatePolicy: "manual"}
	review := storepkg.Installed{OrgID: "org-a", Name: "review", Reference: "localhost:5000/review", Digest: digest, Descriptor: base, Layer: []byte("review"), UpdatePolicy: "manual"}
	review.Descriptor.Requires = []storepkg.PackageRef{{Name: "kit", Digest: digest}}

	if err := s.Install(ctx, review); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("missing dependency: %v", err)
	}
	if err := s.Install(ctx, kit); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(ctx, review); err != nil {
		t.Fatal(err)
	}
	if err := s.Install(ctx, review); !errors.Is(err, storepkg.ErrInstalled) {
		t.Fatalf("duplicate install: %v", err)
	}
	if _, err := s.Get(ctx, "org-b", "kit"); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("cross-org get: %v", err)
	}
	if list, err := s.List(ctx, "org-b"); err != nil || len(list) != 0 {
		t.Fatalf("cross-org list: %v, %v", list, err)
	}
	if err := s.Remove(ctx, "org-a", "kit"); !errors.Is(err, storepkg.ErrRequired) {
		t.Fatalf("remove required package: %v", err)
	}
	if err := s.Remove(ctx, "org-a", "review"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "org-a", "kit"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "org-a", "kit"); !errors.Is(err, storepkg.ErrNotFound) {
		t.Fatalf("remove missing: %v", err)
	}
}
