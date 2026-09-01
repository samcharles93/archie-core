package memory

import "testing"

func TestObservationValidateRequiresIdentityAndContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		obs     Observation
		wantErr bool
	}{
		{"empty identity", Observation{Content: "x"}, true},
		{"empty content", Observation{Identity: "u1"}, true},
		{"both empty", Observation{}, true},
		{"valid", Observation{Identity: "u1", Content: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.obs.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestQueryValidateRequiresIdentity(t *testing.T) {
	t.Parallel()

	if err := (Query{}).Validate(); err == nil {
		t.Fatal("Validate() = nil for empty identity, want error")
	}
	if err := (Query{Identity: "u1"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v for identity-only query, want nil (Text may be empty)", err)
	}
}

func TestManifestValidateAcceptsZeroValue(t *testing.T) {
	t.Parallel()

	if err := (Manifest{}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil: no field is required yet", err)
	}
}
