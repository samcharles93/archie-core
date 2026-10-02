package agent

import "testing"

func TestShippedSoulShipsAStarter(t *testing.T) {
	t.Parallel()

	shipped := ShippedSoul()
	if shipped == "" {
		t.Fatal("ShippedSoul() is empty; first run would seed nothing")
	}
	if !SoulMatchesShipped(shipped, shipped) {
		t.Fatal("ShippedSoul() does not match itself")
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

func TestSoulMatchesShippedNormalisesTransportNoise(t *testing.T) {
	t.Parallel()

	const shipped = "current\nidentity\n"
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "exact", content: shipped, want: true},
		{name: "crlf", content: "current\r\nidentity\r\n", want: true},
		{name: "lone cr", content: "current\ridentity\r", want: true},
		{name: "trailing spaces", content: "current  \nidentity\t\n", want: true},
		{name: "trailing blank lines", content: "current\nidentity\n\n\n", want: true},
		{name: "utf8 bom", content: "\ufeffcurrent\nidentity\n", want: true},
		{name: "an edited word", content: "current\nIDENTITY\n", want: false},
		{name: "leading indent is content", content: "current\n identity\n", want: false},
		{name: "one word removed", content: "current\n", want: false},
		{name: "empty", content: "", want: false},
		{name: "whitespace only", content: "\n\t \n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SoulMatchesShipped(test.content, shipped); got != test.want {
				t.Fatalf("SoulMatchesShipped(%q, %q) = %v, want %v", test.content, shipped, got, test.want)
			}
		})
	}
}

// A body is a user edit until a build actually ships it. Recognising a
// template-shaped body no build ever shipped is the dead producer that left an
// upgrade branch reachable only from tests; this fails if it comes back.
func TestSoulDoesNotRecogniseAnUnshippedTemplate(t *testing.T) {
	t.Parallel()

	const unshipped = "# Archie\n\nOld starter identity.\n"
	if SoulMatchesShipped(unshipped, ShippedSoul()) {
		t.Fatal("an unshipped template body was treated as untouched scaffolding")
	}
}
