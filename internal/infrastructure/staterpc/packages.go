package staterpc

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/samcharles93/archie-core/internal/contracts/state/v1"
	"github.com/samcharles93/archie-core/internal/domain/org"
	"github.com/samcharles93/archie-core/internal/domain/storepkg"
)

func (s *server) InstallPackage(ctx context.Context, request *pb.InstallPackageRequest) (*pb.InstallPackageResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	p, err := s.deps.Packages.InstallPackage(ctx, string(org.OrgFromContext(ctx)), request.GetName(), request.GetReference(), request.GetDigest())
	if err != nil {
		return nil, s.logErr("InstallPackage", err)
	}
	return &pb.InstallPackageResponse{Package: packageProto(p, true)}, nil
}

func (s *server) GetInstalledPackage(ctx context.Context, request *pb.GetInstalledPackageRequest) (*pb.GetInstalledPackageResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	p, err := s.deps.Packages.GetInstalled(ctx, string(org.OrgFromContext(ctx)), request.GetName())
	if err != nil {
		return nil, s.logErr("GetInstalledPackage", err)
	}
	return &pb.GetInstalledPackageResponse{Package: packageProto(p, true)}, nil
}

func (s *server) ListInstalledPackages(ctx context.Context, request *pb.ListInstalledPackagesRequest) (*pb.ListInstalledPackagesResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	packages, err := s.deps.Packages.ListInstalled(ctx, string(org.OrgFromContext(ctx)))
	if err != nil {
		return nil, s.logErr("ListInstalledPackages", err)
	}
	result := &pb.ListInstalledPackagesResponse{}
	for _, p := range packages {
		result.Packages = append(result.Packages, packageProto(p, false))
	}
	return result, nil
}

func (s *server) AcceptPackageAuthority(ctx context.Context, request *pb.AcceptPackageAuthorityRequest) (*pb.AcceptPackageAuthorityResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	p, err := s.deps.Packages.AcceptPackageAuthority(ctx, string(org.OrgFromContext(ctx)), request.GetName(), authorityValue(request.GetAccepted()))
	if err != nil {
		return nil, s.logErr("AcceptPackageAuthority", err)
	}
	return &pb.AcceptPackageAuthorityResponse{Package: packageProto(p, true)}, nil
}

func (s *server) RemoveInstalledPackage(ctx context.Context, request *pb.RemoveInstalledPackageRequest) (*pb.RemoveInstalledPackageResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	if err := s.deps.Packages.RemoveInstalled(ctx, string(org.OrgFromContext(ctx)), request.GetName()); err != nil {
		return nil, s.logErr("RemoveInstalledPackage", err)
	}
	return &pb.RemoveInstalledPackageResponse{}, nil
}

func (c *Client) InstallPackage(ctx context.Context, name, reference, digest string) (storepkg.Installed, error) {
	response, err := c.client.InstallPackage(ctx, &pb.InstallPackageRequest{Name: name, Reference: reference, Digest: digest})
	if err != nil {
		return storepkg.Installed{}, unmapError(err)
	}
	return packageValue(response.GetPackage())
}

func (c *Client) GetInstalled(ctx context.Context, name string) (storepkg.Installed, error) {
	response, err := c.client.GetInstalledPackage(ctx, &pb.GetInstalledPackageRequest{Name: name})
	if err != nil {
		return storepkg.Installed{}, unmapError(err)
	}
	return packageValue(response.GetPackage())
}

func (c *Client) ListInstalled(ctx context.Context) ([]storepkg.Installed, error) {
	response, err := c.client.ListInstalledPackages(ctx, &pb.ListInstalledPackagesRequest{})
	if err != nil {
		return nil, unmapError(err)
	}
	packages := make([]storepkg.Installed, 0, len(response.GetPackages()))
	for _, value := range response.GetPackages() {
		p, err := packageValue(value)
		if err != nil {
			return nil, err
		}
		packages = append(packages, p)
	}
	return packages, nil
}

func (c *Client) RemoveInstalled(ctx context.Context, name string) error {
	_, err := c.client.RemoveInstalledPackage(ctx, &pb.RemoveInstalledPackageRequest{Name: name})
	return unmapError(err)
}

func (c *Client) AcceptPackageAuthority(ctx context.Context, name string, accepted storepkg.Authority) (storepkg.Installed, error) {
	response, err := c.client.AcceptPackageAuthority(ctx, &pb.AcceptPackageAuthorityRequest{
		Name: name, Accepted: authorityProto(accepted),
	})
	if err != nil {
		return storepkg.Installed{}, unmapError(err)
	}
	return packageValue(response.GetPackage())
}

func packageProto(p storepkg.Installed, includeLayer bool) *pb.InstalledPackage {
	descriptor, _ := json.Marshal(p.Descriptor)
	value := &pb.InstalledPackage{
		OrgId: p.OrgID, Name: p.Name, Reference: p.Reference, Digest: p.Digest,
		DescriptorJson: descriptor, UpdatePolicy: p.UpdatePolicy,
	}
	if p.AcceptedAuthority != nil {
		value.AcceptedAuthority = authorityProto(*p.AcceptedAuthority)
	}
	if includeLayer {
		value.Layer = p.Layer
	}
	return value
}

func authorityProto(a storepkg.Authority) *pb.PackageAuthority {
	return &pb.PackageAuthority{
		CredentialServices: a.CredentialServices,
		EgressHosts:        a.EgressHosts,
		ForgePermissions:   a.ForgePermissions,
		Triggers:           a.Triggers,
		Tools:              a.Tools,
		Env:                a.Env,
	}
}

func authorityValue(value *pb.PackageAuthority) storepkg.Authority {
	if value == nil {
		return storepkg.Authority{}
	}
	return storepkg.Authority{
		CredentialServices: value.GetCredentialServices(),
		EgressHosts:        value.GetEgressHosts(),
		ForgePermissions:   value.GetForgePermissions(),
		Triggers:           value.GetTriggers(),
		Tools:              value.GetTools(),
		Env:                value.GetEnv(),
	}
}

func packageValue(value *pb.InstalledPackage) (storepkg.Installed, error) {
	var descriptor storepkg.Descriptor
	if err := json.Unmarshal(value.GetDescriptorJson(), &descriptor); err != nil {
		return storepkg.Installed{}, err
	}
	return storepkg.Installed{
		OrgID: value.GetOrgId(), Name: value.GetName(), Reference: value.GetReference(),
		Digest: value.GetDigest(), Descriptor: descriptor, Layer: value.GetLayer(),
		UpdatePolicy: value.GetUpdatePolicy(), AcceptedAuthority: authorityPtr(value.GetAcceptedAuthority()),
	}, nil
}

func authorityPtr(value *pb.PackageAuthority) *storepkg.Authority {
	if value == nil {
		return nil
	}
	authority := authorityValue(value)
	return &authority
}

func (s *server) ListCatalogue(ctx context.Context, _ *pb.ListCatalogueRequest) (*pb.ListCatalogueResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	catalogue, err := s.deps.Packages.ListCatalogue(ctx)
	if err != nil {
		return nil, s.logErr("ListCatalogue", err)
	}
	out := make([]*pb.CatalogueEntry, 0, len(catalogue.Packages))
	for _, e := range catalogue.Packages {
		out = append(out, &pb.CatalogueEntry{
			Name: e.Name, Surface: e.Surface, Version: e.Version,
			Description: e.Description, Reference: e.Reference, Digest: e.Digest,
		})
	}
	return &pb.ListCatalogueResponse{Packages: out}, nil
}

func (s *server) InstallFromCatalogue(ctx context.Context, request *pb.InstallFromCatalogueRequest) (*pb.InstallFromCatalogueResponse, error) {
	if s.deps.Packages == nil {
		return nil, status.Error(codes.Unavailable, "package store unavailable")
	}
	p, err := s.deps.Packages.InstallFromCatalogue(ctx, string(org.OrgFromContext(ctx)), request.GetName())
	if err != nil {
		return nil, s.logErr("InstallFromCatalogue", err)
	}
	return &pb.InstallFromCatalogueResponse{Package: packageProto(p, true)}, nil
}

func (c *Client) ListCatalogue(ctx context.Context) (storepkg.Catalogue, error) {
	response, err := c.client.ListCatalogue(ctx, &pb.ListCatalogueRequest{})
	if err != nil {
		return storepkg.Catalogue{}, unmapError(err)
	}
	out := storepkg.Catalogue{Packages: make([]storepkg.CatalogueEntry, 0, len(response.GetPackages()))}
	for _, e := range response.GetPackages() {
		out.Packages = append(out.Packages, storepkg.CatalogueEntry{
			Name: e.GetName(), Surface: e.GetSurface(), Version: e.GetVersion(),
			Description: e.GetDescription(), Reference: e.GetReference(), Digest: e.GetDigest(),
		})
	}
	return out, nil
}

func (c *Client) InstallFromCatalogue(ctx context.Context, name string) (storepkg.Installed, error) {
	response, err := c.client.InstallFromCatalogue(ctx, &pb.InstallFromCatalogueRequest{Name: name})
	if err != nil {
		return storepkg.Installed{}, unmapError(err)
	}
	return packageValue(response.GetPackage())
}
