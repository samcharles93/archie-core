package messaging

// ModelDetails carries read-only catalog metadata for one model reference.
type ModelDetails struct {
	Ref             string   `json:"ref"`
	Name            string   `json:"name,omitempty"`
	ContextWindow   int      `json:"context_window,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	Reasoning       bool     `json:"reasoning,omitempty"`
	Tools           bool     `json:"tools,omitempty"`
	Structured      bool     `json:"structured,omitempty"`
	InputModalities []string `json:"input_modalities,omitempty"`
	Attachment      bool     `json:"attachment,omitempty"`
}

// DetailedModelManager optionally enriches model selection confirmation with
// catalog metadata such as context window and modalities.
type DetailedModelManager interface {
	ModelDetails(ref string) (ModelDetails, bool)
}
