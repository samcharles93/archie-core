package tools

import (
	"encoding/json"
	"testing"
)

func TestMultimodalResultJSONFields(t *testing.T) {
	r := MultimodalResult{
		IsMultimodal: true,
		Summary:      "generated a chart",
		SubdirHint:   "tool-output/chart_generator",
		Files:        []string{"tool-output/chart_generator/0.png"},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["is_multimodal"] != true {
		t.Errorf("is_multimodal = %v, want true", got["is_multimodal"])
	}
	if got["summary"] != "generated a chart" {
		t.Errorf("summary = %v", got["summary"])
	}
	if got["subdir_hint"] != "tool-output/chart_generator" {
		t.Errorf("subdir_hint = %v", got["subdir_hint"])
	}
}

func TestMultimodalResultOmitsEmptyOptionalFields(t *testing.T) {
	r := MultimodalResult{IsMultimodal: true, Summary: "no media dir configured"}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["subdir_hint"]; ok {
		t.Error("subdir_hint should be omitted when empty")
	}
	if _, ok := got["files"]; ok {
		t.Error("files should be omitted when empty")
	}
}
