package storepkg

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"

	domain "github.com/samcharles93/archie-core/internal/domain/storepkg"
)

// SignatureAnnotation carries the base64 ed25519 signature over the
// catalogue's layer bytes.
const SignatureAnnotation = "dev.archie.catalogue.signature.v1"

const maxCatalogueBytes = 1 << 20

// DefaultCatalogue is the archipelago marketplace every instance trusts:
// its reference, and the public half of the key that signs it.
func DefaultCatalogue() OCICatalogue {
	return OCICatalogue{
		Reference: "ghcr.io/samcharles93/archipelago/catalogue:latest",
		Key:       mustKey("YL4nvn07uq/5mXZOt9cEmLYu/kB6C2+MLTnbHyoX4Pw="),
	}
}

func mustKey(encoded string) ed25519.PublicKey {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != ed25519.PublicKeySize {
		panic("storepkg: invalid catalogue key")
	}
	return key
}

// OCICatalogue fetches a catalogue artifact and verifies it against Key: a
// one-layer OCI manifest whose layer is the catalogue JSON and whose
// annotation signs that layer. The reference may be a tag; the signature,
// not the tag, is what makes the list trustworthy.
type OCICatalogue struct {
	Reference string
	Key       ed25519.PublicKey
}

var _ domain.CatalogueSource = OCICatalogue{}

func (c OCICatalogue) Fetch(ctx context.Context) (domain.Catalogue, error) {
	if len(c.Key) != ed25519.PublicKeySize {
		return domain.Catalogue{}, errors.New("catalogue: no valid signing key")
	}
	repository, err := remote.NewRepository(c.Reference)
	if err != nil {
		return domain.Catalogue{}, err
	}
	repository.PlainHTTP = loopbackReference(c.Reference)
	_, stream, err := repository.FetchReference(ctx, c.Reference)
	if err != nil {
		return domain.Catalogue{}, fmt.Errorf("catalogue: %w", err)
	}
	manifest, err := readBounded(stream, maxManifestBytes)
	if err != nil {
		return domain.Catalogue{}, err
	}
	var image ocispec.Manifest
	if err := json.Unmarshal(manifest, &image); err != nil {
		return domain.Catalogue{}, fmt.Errorf("catalogue manifest: %w", err)
	}
	if len(image.Layers) != 1 {
		return domain.Catalogue{}, errors.New("catalogue must have one layer")
	}
	layerStream, err := repository.Fetch(ctx, image.Layers[0])
	if err != nil {
		return domain.Catalogue{}, fmt.Errorf("catalogue layer: %w", err)
	}
	layer, err := readBounded(layerStream, maxCatalogueBytes)
	if err != nil {
		return domain.Catalogue{}, err
	}
	return verifyCatalogue(image, layer, c.Key)
}

// verifyCatalogue checks the layer is the one the manifest names and that the
// key signed it, then decodes it.
func verifyCatalogue(image ocispec.Manifest, layer []byte, key ed25519.PublicKey) (domain.Catalogue, error) {
	if contentDigest(layer) != image.Layers[0].Digest.String() {
		return domain.Catalogue{}, errors.New("catalogue layer does not match its manifest digest")
	}
	signature, err := base64.StdEncoding.DecodeString(image.Annotations[SignatureAnnotation])
	if err != nil || !ed25519.Verify(key, layer, signature) {
		return domain.Catalogue{}, errors.New("catalogue signature does not verify")
	}
	decoder := json.NewDecoder(bytes.NewReader(layer))
	decoder.DisallowUnknownFields()
	var catalogue domain.Catalogue
	if err := decoder.Decode(&catalogue); err != nil {
		return domain.Catalogue{}, fmt.Errorf("catalogue: %w", err)
	}
	if err := catalogue.Validate(); err != nil {
		return domain.Catalogue{}, err
	}
	return catalogue, nil
}
