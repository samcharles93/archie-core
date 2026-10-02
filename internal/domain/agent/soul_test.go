package agent

import "testing"

func TestShippedSoulShipsAStarter(t *testing.T) {
	t.Parallel()

	shipped := ShippedSoul()
	if shipped.Default == "" {
		t.Fatal("ShippedSoul().Default is empty; first run would seed nothing")
	}
	if got := shipped.Match(shipped.Default); got != SoulCurrent {
		t.Fatalf("Match(shipped default) = %v, want SoulCurrent", got)
	}
}

func TestShippedSoulIsIndependentOfPersonas(t *testing.T) {
	t.Parallel()

	// The fallback identity has a single owner. If a persona prompt were the
	// starter, retiring the persona catalogue (its own bead) would delete the
	// fallback with it.
	for _, persona := range ShippedPersonas().Personas {
		if persona.Prompt == DefaultSoul {
			t.Fatalf("DefaultSoul is the %q persona prompt; it must be shipped independently", persona.Name)
		}
	}
}

func TestSoulMatch(t *testing.T) {
	t.Parallel()

	const legacy = "# Archie\n\nOld starter identity.\n"
	doc := SoulDocument{Default: "current\nidentity\n", Legacy: []string{legacy}}

	tests := []struct {
		name    string
		content string
		want    SoulMatch
	}{
		{name: "exact default", content: doc.Default, want: SoulCurrent},
		{name: "default with crlf", content: "current\r\nidentity\r\n", want: SoulCurrent},
		{name: "default with lone cr", content: "current\ridentity\r", want: SoulCurrent},
		{name: "default with trailing spaces", content: "current  \nidentity\t\n", want: SoulCurrent},
		{name: "default with trailing blank lines", content: "current\nidentity\n\n\n", want: SoulCurrent},
		{name: "default with utf8 bom", content: "\ufeffcurrent\nidentity\n", want: SoulCurrent},
		{name: "legacy template", content: legacy, want: SoulSuperseded},
		{name: "legacy template with crlf", content: "# Archie\r\n\r\nOld starter identity.\r\n", want: SoulSuperseded},
		{name: "legacy template with trailing whitespace", content: "# Archie\n\nOld starter identity.   \n", want: SoulSuperseded},
		{name: "an edited word", content: "current\nIDENTITY\n", want: SoulEdit},
		{name: "leading indent is content", content: "current\n identity\n", want: SoulEdit},
		{name: "one word removed", content: "current\n", want: SoulEdit},
		{name: "empty", content: "", want: SoulEdit},
		{name: "whitespace only", content: "\n\t \n", want: SoulEdit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := doc.Match(test.content); got != test.want {
				t.Fatalf("Match(%q) = %v, want %v", test.content, got, test.want)
			}
		})
	}
}

func TestSoulMatchTreatsMissingDefaultAsEdited(t *testing.T) {
	t.Parallel()

	var doc SoulDocument
	if got := doc.Match("anything"); got != SoulEdit {
		t.Fatalf("empty document Match = %v, want SoulEdit", got)
	}
}
