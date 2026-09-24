package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/edastore"
)

func readRawSourceSecret(t *testing.T, pool *pgxpool.Pool, path string) string {
	t.Helper()
	var stored string
	if err := pool.QueryRow(t.Context(), "SELECT secret FROM sources WHERE path = $1", path).Scan(&stored); err != nil {
		t.Fatalf("read raw secret: %v", err)
	}
	return stored
}

// A new source keeps its generated path, is signed, stores its secret as a
// cipher envelope and reads it back as plaintext.
func TestSourceRoundTripEncryptsSecret(t *testing.T) {
	cipher, err := edastore.NewBindingCipher(edaTestKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher() error = %v", err)
	}
	pool, s := edaWithCipher(t, cipher)
	in, err := source.New("")
	if err != nil {
		t.Fatalf("source.New() error = %v", err)
	}
	if err := s.InsertSource(t.Context(), in); err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	if raw := readRawSourceSecret(t, pool, in.Path); raw == in.Secret || raw == "" {
		t.Fatalf("stored secret = %q, want a cipher envelope", raw)
	}
	got, err := s.GetSource(t.Context(), in.Path)
	if err != nil || got == nil {
		t.Fatalf("GetSource() = %v, %v", got, err)
	}
	if got.Path != in.Path || got.Signing != source.SigningSigned || got.Secret != in.Secret {
		t.Fatalf("GetSource() = %+v, want path %q signed with the plaintext secret", got, in.Path)
	}
	list, err := s.ListSources(t.Context())
	if err != nil || len(list) != 1 || list[0].Secret != in.Secret {
		t.Fatalf("ListSources() = %+v, %v", list, err)
	}
}

func TestSourceWritesRefuseBadState(t *testing.T) {
	s := edaFor(t)
	taken := source.Source{Path: "sentry", Signing: source.SigningSigned, Secret: "k"}
	if err := s.InsertSource(t.Context(), taken); err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	tests := []struct {
		name  string
		write func() error
		want  error
	}{
		{"taken custom path", func() error { return s.InsertSource(t.Context(), taken) }, storecontract.ErrSourcePathTaken},
		{"stale signing", func() error {
			return s.SetSourceSigning(t.Context(), "sentry", source.SigningUnsignedPending, source.SigningUnsigned)
		}, storecontract.ErrSourceSigningStale},
		{"signing on unknown path", func() error {
			return s.SetSourceSigning(t.Context(), "nope", source.SigningSigned, source.SigningUnsignedPending)
		}, storecontract.ErrSourceNotFound},
		{"secret on unknown path", func() error { return s.SetSourceSecret(t.Context(), "nope", "k") }, storecontract.ErrSourceNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.write(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
	got, err := s.GetSource(t.Context(), "nope")
	if err != nil || got != nil {
		t.Fatalf("GetSource(unknown) = %v, %v; want nil, nil", got, err)
	}
}

func TestSourceSigningAndSecretWrites(t *testing.T) {
	s := edaFor(t)
	if err := s.InsertSource(t.Context(), source.Source{Path: "fw", Signing: source.SigningSigned}); err != nil {
		t.Fatalf("InsertSource() error = %v", err)
	}
	if err := s.SetSourceSigning(t.Context(), "fw", source.SigningSigned, source.SigningUnsignedPending); err != nil {
		t.Fatalf("SetSourceSigning() error = %v", err)
	}
	if err := s.SetSourceSecret(t.Context(), "fw", "newsecret"); err != nil {
		t.Fatalf("SetSourceSecret() error = %v", err)
	}
	got, err := s.GetSource(t.Context(), "fw")
	if err != nil || got.Signing != source.SigningUnsignedPending || got.Secret != "newsecret" {
		t.Fatalf("GetSource() = %+v, %v", got, err)
	}
}

func TestCaptureUnsignedRoundTrip(t *testing.T) {
	s := edaFor(t)
	if _, err := s.InsertCapture(t.Context(), storecontract.CapturedEvent{Source: "fw", Unsigned: true}, 0, 0); err != nil {
		t.Fatalf("InsertCapture() error = %v", err)
	}
	for name, list := range map[string]func() ([]storecontract.CapturedEvent, error){
		"ListCaptures": func() ([]storecontract.CapturedEvent, error) { return s.ListCaptures(t.Context(), 10) },
		"ListUndispatchedCaptures": func() ([]storecontract.CapturedEvent, error) {
			return s.ListUndispatchedCaptures(t.Context(), []string{"fw"}, 10)
		},
	} {
		got, err := list()
		if err != nil || len(got) != 1 || !got[0].Unsigned {
			t.Errorf("%s() = %+v, %v; want one unsigned capture", name, got, err)
		}
	}
}

// Existing source strings become sources: a bound source takes its binding's
// secret, a capture-only source gets none, and an existing source is kept.
func TestDeriveSourcesFromExistingRows(t *testing.T) {
	pool, q := migrated(t)
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO bindings (id, name, source, secret) VALUES ('b1', 'n', 'sentry', 'envelope')`,
		`INSERT INTO captures (id, source, received_at) VALUES ('c1', 'sentry', now()), ('c2', 'firewall', now()), ('c3', 'kept', now())`,
		`INSERT INTO sources (path, signing, secret) VALUES ('kept', 'unsigned', 'x')`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if err := q.DeriveSources(ctx); err != nil {
		t.Fatalf("DeriveSources() error = %v", err)
	}
	want := map[string][2]string{
		"sentry":   {"signed", "envelope"},
		"firewall": {"signed", ""},
		"kept":     {"unsigned", "x"},
	}
	rows, err := q.ListSources(ctx)
	if err != nil {
		t.Fatalf("ListSources() error = %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("derived %d sources, want %d: %+v", len(rows), len(want), rows)
	}
	for _, r := range rows {
		if w := want[r.Path]; w != [2]string{r.Signing, r.Secret} {
			t.Errorf("source %q = (%s, %q), want %v", r.Path, r.Signing, r.Secret, w)
		}
	}
}
