package storepkg

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeRegistry struct {
	descriptor Descriptor
	err        error
	calls      int
}

func (r *fakeRegistry) Fetch(context.Context, string, string) (Descriptor, []byte, error) {
	r.calls++
	return r.descriptor, nil, r.err
}

type fakeInstalledStore struct {
	records map[string]Installed
	err     error
}

func (s *fakeInstalledStore) Install(_ context.Context, p Installed) error {
	if s.err != nil {
		return s.err
	}
	s.records[p.OrgID+"/"+p.Name] = p
	return nil
}

func (s *fakeInstalledStore) Get(_ context.Context, org, name string) (Installed, error) {
	p, ok := s.records[org+"/"+name]
	if !ok {
		return Installed{}, ErrNotFound
	}
	return p, nil
}

func (s *fakeInstalledStore) List(_ context.Context, org string) ([]Installed, error) {
	var out []Installed
	for _, p := range s.records {
		if p.OrgID == org {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *fakeInstalledStore) Remove(_ context.Context, org, name string) error {
	if _, ok := s.records[org+"/"+name]; !ok {
		return ErrNotFound
	}
	delete(s.records, org+"/"+name)
	return nil
}

func TestServiceInstall(t *testing.T) {
	ctx := context.Background()
	valid, err := Decode(strings.NewReader(validDescriptor))
	if err != nil {
		t.Fatal(err)
	}
	valid.Requires = nil
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name, org, ref, pin string
		descriptor          Descriptor
		registryErr         error
		wantErr             bool
	}{
		{name: "pinned install", org: "org-a", ref: "localhost:5000/review", pin: digest, descriptor: valid},
		{name: "tag refused", org: "org-a", ref: "localhost:5000/review", pin: "latest", descriptor: valid, wantErr: true},
		{name: "missing org", ref: "localhost:5000/review", pin: digest, descriptor: valid, wantErr: true},
		{name: "registry failure", org: "org-a", ref: "localhost:5000/review", pin: digest, descriptor: valid, registryErr: errors.New("missing"), wantErr: true},
		{name: "invalid descriptor", org: "org-a", ref: "localhost:5000/review", pin: digest, descriptor: Descriptor{}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &fakeRegistry{descriptor: tc.descriptor, err: tc.registryErr}
			store := &fakeInstalledStore{records: map[string]Installed{}}
			got, err := (Service{Registry: registry, Store: store}).Install(ctx, tc.org, "review", tc.ref, tc.pin)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Install error = %v", err)
			}
			if tc.wantErr {
				if len(store.records) != 0 {
					t.Fatal("failed install persisted a record")
				}
				return
			}
			if got.Digest != digest || got.OrgID != tc.org || got.UpdatePolicy != "manual" {
				t.Fatalf("installed = %#v", got)
			}
			if _, err := store.Get(ctx, "org-b", "review"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("other org sees package: %v", err)
			}
		})
	}
}

func TestServiceInstallRequiresPinnedDependency(t *testing.T) {
	ctx := context.Background()
	valid, err := Decode(strings.NewReader(validDescriptor))
	if err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cases := []struct {
		name      string
		installed map[string]Installed
		wantErr   bool
	}{
		{name: "missing", installed: map[string]Installed{}, wantErr: true},
		{name: "wrong org", installed: map[string]Installed{"other/review-kit": {OrgID: "other", Name: "review-kit", Digest: digest}}, wantErr: true},
		{name: "wrong digest", installed: map[string]Installed{"org/review-kit": {OrgID: "org", Name: "review-kit", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}, wantErr: true},
		{name: "matching", installed: map[string]Installed{"org/review-kit": {OrgID: "org", Name: "review-kit", Digest: digest}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeInstalledStore{records: tc.installed}
			_, err := (Service{Registry: &fakeRegistry{descriptor: valid}, Store: store}).Install(ctx, "org", "review", "localhost:5000/review", digest)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Install error = %v", err)
			}
		})
	}
}

func TestServiceRemove(t *testing.T) {
	ctx := context.Background()
	store := &fakeInstalledStore{records: map[string]Installed{
		"org-a/review": {OrgID: "org-a", Name: "review"},
		"org-b/review": {OrgID: "org-b", Name: "review"},
	}}
	svc := Service{Store: store}
	if err := svc.Remove(ctx, "org-a", "review"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "org-a", "review"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed package remains: %v", err)
	}
	if _, err := store.Get(ctx, "org-b", "review"); err != nil {
		t.Fatalf("other org package removed: %v", err)
	}
}
