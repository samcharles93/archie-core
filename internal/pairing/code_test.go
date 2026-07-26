package pairing

import (
	"strings"
	"testing"
)

func TestGenerateCodeLength(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != CodeLength {
		t.Errorf("len(code) = %d, want %d", len(code), CodeLength)
	}
}

func TestGenerateCodeUsesOnlyAlphabetChars(t *testing.T) {
	for range 200 {
		code, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range code {
			if !strings.ContainsRune(Alphabet, r) {
				t.Fatalf("code %q contains character %q not in Alphabet %q", code, r, Alphabet)
			}
		}
	}
}

func TestGenerateCodeExcludesAmbiguousChars(t *testing.T) {
	// 0, O, 1, I must never appear  --  they're visually confusable when a
	// human reads a code aloud or types it from a screen.
	ambiguous := "0O1I"
	for _, r := range ambiguous {
		if strings.ContainsRune(Alphabet, r) {
			t.Fatalf("Alphabet %q contains ambiguous character %q", Alphabet, r)
		}
	}
}

func TestAlphabetHas32UniqueChars(t *testing.T) {
	if len(Alphabet) != 32 {
		t.Fatalf("len(Alphabet) = %d, want 32", len(Alphabet))
	}
	seen := make(map[rune]bool, 32)
	for _, r := range Alphabet {
		if seen[r] {
			t.Fatalf("Alphabet contains duplicate character %q", r)
		}
		seen[r] = true
	}
}

func TestGenerateCodeIsRandom(t *testing.T) {
	seen := make(map[string]bool)
	for range 500 {
		code, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		if seen[code] {
			// Not impossible with 32^8 codes, but astronomically
			// unlikely across 500 draws  --  a collision here almost
			// certainly means the generator isn't actually random.
			t.Fatalf("code %q generated twice within 500 draws", code)
		}
		seen[code] = true
	}
}

func TestGenerateCodeUppercaseOnly(t *testing.T) {
	code, err := GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	if code != strings.ToUpper(code) {
		t.Errorf("code %q is not uppercase", code)
	}
}
