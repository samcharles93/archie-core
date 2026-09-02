package image

import "testing"

func TestCapabilityValidateRejectsEditWithoutInputs(t *testing.T) {
	t.Parallel()
	c := Capability{Edit: true, MaxInputs: 0}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for edit with MaxInputs 0")
	}
}

func TestCapabilityValidateRejectsMaskWithoutEdit(t *testing.T) {
	t.Parallel()
	c := Capability{Mask: true}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for mask without edit")
	}
}

func TestCapabilityValidateAcceptsGenerateOnly(t *testing.T) {
	t.Parallel()
	c := Capability{Generate: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestCapabilityValidateAcceptsEditWithMask(t *testing.T) {
	t.Parallel()
	c := Capability{Edit: true, MaxInputs: 1, Mask: true}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestGenerateRequestValidateRequiresPrompt(t *testing.T) {
	t.Parallel()
	if err := (GenerateRequest{}).Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for empty prompt")
	}
	if err := (GenerateRequest{Prompt: "a cat"}).Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestEditRequestValidateRequiresPromptAndInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  EditRequest
		want bool // true if a valid request
	}{
		{"empty", EditRequest{}, false},
		{"no inputs", EditRequest{Prompt: "add a hat"}, false},
		{"no prompt", EditRequest{Inputs: []ImageData{{Bytes: []byte("x")}}}, false},
		{"valid", EditRequest{Prompt: "add a hat", Inputs: []ImageData{{Bytes: []byte("x")}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.req.Validate()
			if (err == nil) != tc.want {
				t.Fatalf("Validate() = %v, want valid=%v", err, tc.want)
			}
		})
	}
}
