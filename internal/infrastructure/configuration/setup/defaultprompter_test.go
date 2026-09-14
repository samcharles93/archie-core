package setup

import (
	"context"
	"testing"
)

func TestDefaultPrompter(t *testing.T) {
	var p DefaultPrompter

	if idx, err := p.Select(context.Background(), "q", []string{"a", "b"}); err != nil || idx != 0 {
		t.Errorf("Select = (%d, %v), want (0, nil)", idx, err)
	}
	if got, err := p.ReadLine(context.Background(), "q", "default"); err != nil || got != "default" {
		t.Errorf("ReadLine = (%q, %v), want (\"default\", nil)", got, err)
	}
	if got, err := p.ReadSecret(context.Background(), "q"); err != nil || got != "" {
		t.Errorf("ReadSecret = (%q, %v), want (\"\", nil)", got, err)
	}
	if got, err := p.Confirm(context.Background(), "q", true); err != nil || !got {
		t.Errorf("Confirm(defaultYes=true) = (%v, %v), want (true, nil)", got, err)
	}
	if got, err := p.Confirm(context.Background(), "q", false); err != nil || got {
		t.Errorf("Confirm(defaultYes=false) = (%v, %v), want (false, nil)", got, err)
	}
}
