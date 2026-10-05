package storepkg

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// A catalogue is trusted only when its layer is the one the manifest names
// and the configured key signed exactly those bytes.
func TestVerifyCatalogue(t *testing.T) {
	trusted, signer, _ := ed25519.GenerateKey(nil)
	_, stranger, _ := ed25519.GenerateKey(nil)
	valid := []byte(`{"packages":[{"name":"github","reference":"ghcr.io/x/github","digest":"sha256:` + zeros + `"}]}`)
	unpinned := []byte(`{"packages":[{"name":"github","reference":"ghcr.io/x/github","digest":"latest"}]}`)

	manifest := func(layer []byte, signature string) ocispec.Manifest {
		return ocispec.Manifest{
			Layers:      []ocispec.Descriptor{{Digest: digest.FromBytes(layer)}},
			Annotations: map[string]string{SignatureAnnotation: signature},
		}
	}
	sign := func(key ed25519.PrivateKey, layer []byte) string {
		return base64.StdEncoding.EncodeToString(ed25519.Sign(key, layer))
	}
	tampered := append([]byte(nil), valid...)
	tampered[len(tampered)-3] = 'x'

	cases := []struct {
		name     string
		manifest ocispec.Manifest
		layer    []byte
		ok       bool
	}{
		{"signed by the trusted key", manifest(valid, sign(signer, valid)), valid, true},
		{"layer swapped after signing", manifest(valid, sign(signer, valid)), tampered, false},
		{"layer and digest swapped together", manifest(tampered, sign(signer, valid)), tampered, false},
		{"signed by another key", manifest(valid, sign(stranger, valid)), valid, false},
		{"unsigned", manifest(valid, ""), valid, false},
		{"an entry without a digest pin", manifest(unpinned, sign(signer, unpinned)), unpinned, false},
	}
	for _, tc := range cases {
		_, err := verifyCatalogue(tc.manifest, tc.layer, trusted)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

const zeros = "0000000000000000000000000000000000000000000000000000000000000000"
