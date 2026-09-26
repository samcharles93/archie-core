package storepkg

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDecodeOCIRejectsTamperingAndUndeclaredFiles(t *testing.T) {
	layer := packageLayer(t, "workflows/review.yaml", []byte("name: review"))
	manifest := packageManifest(t, layer)
	extra := packageLayer(t, "extra.yaml", []byte("extra"))
	extraManifest := packageManifest(t, extra)
	cases := []struct {
		name            string
		manifest, layer []byte
		pin             string
		wantErr         bool
	}{
		{name: "valid", manifest: manifest, layer: layer, pin: digestOf(manifest)},
		{name: "wrong manifest pin", manifest: manifest, layer: layer, pin: digestOf([]byte("other")), wantErr: true},
		{name: "tampered layer", manifest: manifest, layer: append(bytes.Clone(layer), 'x'), pin: digestOf(manifest), wantErr: true},
		{name: "undeclared file", manifest: extraManifest, layer: extra, pin: digestOf(extraManifest), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := decodeOCI(tc.manifest, tc.layer, tc.pin)
			if (err != nil) != tc.wantErr {
				t.Fatalf("decodeOCI error = %v", err)
			}
		})
	}
}

func TestLocalRegistryFetchPinnedPackage(t *testing.T) {
	layer := packageLayer(t, "workflows/review.yaml", []byte("name: review"))
	manifest := packageManifest(t, layer)
	pin := digestOf(manifest)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/":
			w.WriteHeader(http.StatusOK)
		case "/v2/review/manifests/" + pin:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", pin)
			w.Header().Set("Content-Length", fmt.Sprint(len(manifest)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(manifest)
			}
		case "/v2/review/blobs/" + digestOf(layer):
			w.Header().Set("Content-Type", ocispec.MediaTypeImageLayer)
			w.Header().Set("Content-Length", fmt.Sprint(len(layer)))
			if r.Method != http.MethodHead {
				_, _ = w.Write(layer)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	reference := strings.TrimPrefix(server.URL, "http://") + "/review"
	descriptor, fetched, err := (LocalRegistry{}).Fetch(t.Context(), reference, pin)
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.DisplayName != "Review" || !bytes.Equal(fetched, layer) {
		t.Fatalf("fetched descriptor/layer = %#v, %d bytes", descriptor, len(fetched))
	}
	if _, _, err := (LocalRegistry{}).Fetch(t.Context(), reference, digestOf([]byte("wrong"))); err == nil {
		t.Fatal("wrong pin accepted")
	}
}

func packageLayer(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func packageManifest(t *testing.T, layer []byte) []byte {
	t.Helper()
	manifest := ocispec.Manifest{
		SchemaVersion: 2,
		Annotations:   map[string]string{"dev.archie.package.v1": "apiVersion: dev.archie.package.v1\ndisplayName: Review\nversion: 1\ncontributes:\n  workflows: [review]\nfiles:\n  - path: workflows/review.yaml\n    mode: 0644\n"},
		Layers:        []ocispec.Descriptor{{MediaType: ocispec.MediaTypeImageLayer, Digest: digest.Digest(digestOf(layer)), Size: int64(len(layer))}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
