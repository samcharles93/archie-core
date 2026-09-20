package agent

import "testing"

func TestPersonaCollectionValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		collection PersonaCollection
		wantErr    bool
	}{
		{name: "valid", collection: PersonaCollection{Default: "archie", Personas: []Persona{{Name: "archie", Prompt: "baseline"}, {Name: "concise", Prompt: "brief"}}}},
		{name: "missing default", collection: PersonaCollection{Default: "missing", Personas: []Persona{{Name: "archie", Prompt: "baseline"}}}, wantErr: true},
		{name: "duplicate normalized name", collection: PersonaCollection{Default: "archie", Personas: []Persona{{Name: "Archie", Prompt: "baseline"}, {Name: " archie ", Prompt: "other"}}}, wantErr: true},
		{name: "empty prompt", collection: PersonaCollection{Default: "archie", Personas: []Persona{{Name: "archie"}}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.collection.Validate(); (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
