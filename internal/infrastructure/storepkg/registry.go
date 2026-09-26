// Package storepkg fetches digest-pinned Archie packages from OCI registries.
package storepkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	domain "github.com/samcharles93/archie-core/internal/domain/storepkg"
)

const (
	maxManifestBytes = 256 << 10
	maxPackageBytes  = 2 << 20
)

// LocalRegistry accepts a local OCI registry over HTTP and verifies the
// manifest and sole layer before returning declarative package content.
type LocalRegistry struct{}

var _ domain.Registry = LocalRegistry{}

func (LocalRegistry) Fetch(ctx context.Context, reference, pin string) (domain.Descriptor, []byte, error) {
	if !localReference(reference) {
		return domain.Descriptor{}, nil, errors.New("package registry must be local")
	}
	repository, err := remote.NewRepository(reference)
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	repository.PlainHTTP = true
	_, stream, err := repository.FetchReference(ctx, pin)
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	manifest, err := readBounded(stream, maxManifestBytes)
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	var image ocispec.Manifest
	if err := json.Unmarshal(manifest, &image); err != nil {
		return domain.Descriptor{}, nil, err
	}
	if len(image.Layers) != 1 {
		return domain.Descriptor{}, nil, errors.New("archie package must have one layer")
	}
	layerStream, err := repository.Fetch(ctx, image.Layers[0])
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	layer, err := readBounded(layerStream, maxPackageBytes)
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	return decodeOCI(manifest, layer, pin)
}

func localReference(reference string) bool {
	host, _, ok := strings.Cut(reference, "/")
	if !ok {
		return false
	}
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func readBounded(stream io.ReadCloser, limit int) ([]byte, error) {
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, errors.New("package content exceeds the size limit")
	}
	return data, nil
}

func decodeOCI(manifest, layer []byte, pin string) (domain.Descriptor, []byte, error) {
	if contentDigest(manifest) != pin {
		return domain.Descriptor{}, nil, errors.New("package manifest digest mismatch")
	}
	var image ocispec.Manifest
	if err := json.Unmarshal(manifest, &image); err != nil {
		return domain.Descriptor{}, nil, fmt.Errorf("decode OCI manifest: %w", err)
	}
	if image.SchemaVersion != 2 || len(image.Layers) != 1 {
		return domain.Descriptor{}, nil, errors.New("archie package must be an OCI image with one layer")
	}
	blob := image.Layers[0]
	if blob.Size != int64(len(layer)) || blob.Digest.String() != contentDigest(layer) {
		return domain.Descriptor{}, nil, errors.New("package layer digest or size mismatch")
	}
	raw, ok := image.Annotations[domain.APIVersion]
	if !ok {
		return domain.Descriptor{}, nil, errors.New("archie package descriptor annotation is missing")
	}
	descriptor, err := domain.Decode(strings.NewReader(raw))
	if err != nil {
		return domain.Descriptor{}, nil, err
	}
	if err := validateLayer(layer, blob.MediaType, descriptor.Files); err != nil {
		return domain.Descriptor{}, nil, err
	}
	return descriptor, layer, nil
}

func validateLayer(layer []byte, mediaType string, files []domain.File) error {
	var reader io.Reader = bytes.NewReader(layer)
	switch mediaType {
	case ocispec.MediaTypeImageLayer:
	case ocispec.MediaTypeImageLayerGzip:
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	default:
		return fmt.Errorf("unsupported package layer media type %q", mediaType)
	}
	declared := make(map[string]uint32, len(files))
	for _, file := range files {
		declared[file.Path] = file.Mode
	}
	found := make(map[string]bool, len(files))
	tarReader := tar.NewReader(io.LimitReader(reader, maxPackageBytes+1))
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read package layer: %w", err)
		}
		if err := validateLayerEntry(header, declared, found); err != nil {
			return err
		}
		found[header.Name] = true
		if _, err := io.Copy(io.Discard, tarReader); err != nil {
			return err
		}
	}
	if len(found) != len(files) {
		return errors.New("package layer is missing a declared file")
	}
	return nil
}

func validateLayerEntry(header *tar.Header, declared map[string]uint32, found map[string]bool) error {
	if header.Typeflag != tar.TypeReg {
		return fmt.Errorf("package layer contains non-regular file %q", header.Name)
	}
	if path.Clean(header.Name) != header.Name || strings.HasPrefix(header.Name, "../") {
		return fmt.Errorf("package layer contains unsafe path %q", header.Name)
	}
	mode, ok := declared[header.Name]
	if !ok || found[header.Name] {
		return fmt.Errorf("package layer contains undeclared or duplicate file %q", header.Name)
	}
	if uint32(header.Mode)&0o777 != mode&0o777 {
		return fmt.Errorf("package file %q mode differs from descriptor", header.Name)
	}
	return nil
}

func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
