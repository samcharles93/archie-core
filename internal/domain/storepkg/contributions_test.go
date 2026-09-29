package storepkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"strings"
	"testing"
)

// contributedPackageDescriptor declares one contributed workflow file.
const contributedPackage = `apiVersion: dev.archie.package.v1
displayName: Review pack
version: "1"
contributes:
  workflows:
    - workflows/review.yaml
files:
  - path: workflows/review.yaml
    mode: 0644
`

func buildLayer(t *testing.T, files map[string][]byte, packed bool) []byte {
	t.Helper()
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, name := range sortedFileNames(files) {
		if err := writer.WriteHeader(&tar.Header{
			Typeflag: tar.TypeReg, Name: name, Size: int64(len(files[name])), Mode: 0o644,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if !packed {
		return raw.Bytes()
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sortedFileNames(files map[string][]byte) []string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

func TestFilesFromLayer(t *testing.T) {
	files := map[string][]byte{
		"workflows/review.yaml": []byte("id: review\nsteps:\n"),
		"workflows/other.yaml":  []byte("id: other\nsteps:\n"),
	}
	for _, form := range []struct {
		name  string
		layer []byte
	}{
		{name: "plain tar layer", layer: buildLayer(t, files, false)},
		{name: "gzipped tar layer", layer: buildLayer(t, files, true)},
	} {
		t.Run(form.name, func(t *testing.T) {
			extracted, err := FilesFromLayer(form.layer)
			if err != nil {
				t.Fatal(err)
			}
			if len(extracted) != 2 || !bytes.Equal(extracted["workflows/review.yaml"], files["workflows/review.yaml"]) {
				t.Fatalf("files = %v", extracted)
			}
		})
	}
	t.Run("a non-tar layer is an error", func(t *testing.T) {
		if _, err := FilesFromLayer([]byte("not a tar")); err == nil {
			t.Fatal("non-tar layer extracted")
		}
	})
}

// fakeProjector records the contents one family receives, and the order the
// service applies and withdraws them.
type fakeProjector struct {
	contents    map[string]map[string][]byte // org/name -> contributed name -> content
	applyCalled bool
	withdrawn   []string
	applyErr    error
	withdrawErr error
}

func (p *fakeProjector) Apply(_ context.Context, orgID, name, _ string, contents map[string][]byte) error {
	p.applyCalled = true
	if p.applyErr != nil {
		return p.applyErr
	}
	p.contents[orgID+"/"+name] = contents
	return nil
}

func (p *fakeProjector) Withdraw(_ context.Context, orgID, name, _ string) error {
	p.withdrawn = append(p.withdrawn, orgID+"/"+name)
	if p.withdrawErr != nil {
		return p.withdrawErr
	}
	delete(p.contents, orgID+"/"+name)
	return nil
}

func TestServiceInstallProjectsContributions(t *testing.T) {
	ctx := context.Background()
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	descriptor, err := Decode(strings.NewReader(contributedPackage))
	if err != nil {
		t.Fatal(err)
	}
	workflowYAML := []byte("id: review\nsteps:\n")
	layer := buildLayer(t, map[string][]byte{"workflows/review.yaml": workflowYAML}, false)
	service := func(store Repository, projector *fakeProjector) Service {
		if projector == nil {
			return Service{Registry: &fakeLayeredRegistry{descriptor: descriptor, layer: layer}, Store: store}
		}
		return Service{Registry: &fakeLayeredRegistry{descriptor: descriptor, layer: layer}, Store: store, Projections: map[string]FamilyProjector{FamilyWorkflows: projector}}
	}

	t.Run("a contributed workflow reaches the family projector", func(t *testing.T) {
		projector := &fakeProjector{contents: map[string]map[string][]byte{}}
		store := &fakeInstalledStore{records: map[string]Installed{}}
		if _, err := service(store, projector).Install(ctx, "org-a", "review", "localhost:5000/review", digest); err != nil {
			t.Fatal(err)
		}
		if !projector.applyCalled {
			t.Fatal("projector never applied")
		}
		if got := projector.contents["org-a/review"]["workflows/review.yaml"]; !bytes.Equal(got, workflowYAML) {
			t.Fatalf("projected content = %q", got)
		}
	})
	t.Run("a failed projection is not a silent half install", func(t *testing.T) {
		projector := &fakeProjector{contents: map[string]map[string][]byte{}, applyErr: errors.New("no resource kind")}
		store := &fakeInstalledStore{records: map[string]Installed{}}
		if _, err := service(store, projector).Install(ctx, "org-a", "review", "localhost:5000/review", digest); err == nil {
			t.Fatal("install succeeded although the projection failed")
		}
		if _, ok := store.records["org-a/review"]; ok {
			t.Fatal("failed projection left an installed package without its contributions")
		}
	})
	t.Run("a contribution the layer does not carry refuses the install", func(t *testing.T) {
		empty := buildLayer(t, map[string][]byte{}, false)
		projector := &fakeProjector{contents: map[string]map[string][]byte{}}
		svc := Service{Registry: &fakeLayeredRegistry{descriptor: descriptor, layer: empty}, Store: &fakeInstalledStore{records: map[string]Installed{}}, Projections: map[string]FamilyProjector{FamilyWorkflows: projector}}
		if _, err := svc.Install(ctx, "org-a", "review", "localhost:5000/review", digest); err == nil {
			t.Fatal("contributed name without layer content installed")
		}
	})
	t.Run("removal withdraws before the record goes", func(t *testing.T) {
		projector := &fakeProjector{contents: map[string]map[string][]byte{}}
		store := &fakeInstalledStore{records: map[string]Installed{"org-a/review": {OrgID: "org-a", Name: "review", Descriptor: descriptor, Digest: digest, UpdatePolicy: "manual"}}}
		if err := service(store, projector).Remove(ctx, "org-a", "review"); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.records["org-a/review"]; ok {
			t.Fatal("removed package record remains")
		}
		if len(projector.withdrawn) != 1 || projector.withdrawn[0] != "org-a/review" {
			t.Fatalf("withdrawn = %v", projector.withdrawn)
		}
	})
	t.Run("a failed withdraw keeps the whole installation", func(t *testing.T) {
		projector := &fakeProjector{contents: map[string]map[string][]byte{}, withdrawErr: errors.New("version conflict")}
		store := &fakeInstalledStore{records: map[string]Installed{"org-a/review": {OrgID: "org-a", Name: "review", Descriptor: descriptor, Digest: digest, UpdatePolicy: "manual"}}}
		if err := service(store, projector).Remove(ctx, "org-a", "review"); err == nil {
			t.Fatal("removal succeeded although the withdrawal failed")
		}
		if _, ok := store.records["org-a/review"]; !ok {
			t.Fatal("package removed but its withdrawable contributions were already gone")
		}
	})
	t.Run("a family without a projector installs unprojected", func(t *testing.T) {
		store := &fakeInstalledStore{records: map[string]Installed{}}
		if _, err := service(store, nil).Install(ctx, "org-a", "review", "localhost:5000/review", digest); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.records["org-a/review"]; !ok {
			t.Fatal("non-projected family refused to install")
		}
	})
}
