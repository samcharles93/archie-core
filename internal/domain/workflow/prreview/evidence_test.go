package prreview

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

func TestExtractEvidenceQuotesTheCitedLines(t *testing.T) {
	t.Parallel()

	var builder strings.Builder
	for line := 1; line <= 200; line++ {
		fmt.Fprintf(&builder, "line %d\n", line)
	}
	fsys := fstest.MapFS{
		"src/big.go":   {Data: []byte(builder.String())},
		"src/small.go": {Data: []byte("package small\n")},
		"src/logo.png": {Data: []byte("\x89PNG\r\n\x1a\nbinary")},
	}

	t.Run("a cited line is quoted with its context", func(t *testing.T) {
		t.Parallel()

		pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "src/big.go", LineStart: 100, LineEnd: 100, Title: "t"})
		if err != nil {
			t.Fatalf("ExtractEvidence() error = %v", err)
		}
		for _, want := range []string{"100: line 100", "70: line 70", "130: line 130"} {
			if !strings.Contains(pkg.PrimaryCode, want) {
				t.Errorf("PrimaryCode lacks %q:\n%s", want, pkg.PrimaryCode)
			}
		}
		for _, unwanted := range []string{"69: line 69", "131: line 131"} {
			if strings.Contains(pkg.PrimaryCode, unwanted) {
				t.Errorf("PrimaryCode quotes beyond the context window: %q\n%s", unwanted, pkg.PrimaryCode)
			}
		}
	})

	t.Run("a cited range is quoted whole", func(t *testing.T) {
		t.Parallel()

		pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "src/big.go", LineStart: 100, LineEnd: 102, Title: "t"})
		if err != nil {
			t.Fatalf("ExtractEvidence() error = %v", err)
		}
		if !strings.Contains(pkg.PrimaryCode, "102: line 102") || !strings.Contains(pkg.PrimaryCode, "132: line 132") {
			t.Errorf("PrimaryCode does not quote the cited range:\n%s", pkg.PrimaryCode)
		}
	})

	t.Run("a file the snapshot does not have is quoted as nothing", func(t *testing.T) {
		t.Parallel()

		pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "src/gone.go", LineStart: 1, LineEnd: 1, Title: "t"})
		if err != nil {
			t.Fatalf("ExtractEvidence() error = %v", err)
		}
		if pkg.PrimaryCode != "" {
			t.Errorf("PrimaryCode = %q, want no code for a file that is not there", pkg.PrimaryCode)
		}
	})

	t.Run("a binary file is quoted as nothing", func(t *testing.T) {
		t.Parallel()

		pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "src/logo.png", LineStart: 1, LineEnd: 1, Title: "t"})
		if err != nil {
			t.Fatalf("ExtractEvidence() error = %v", err)
		}
		if pkg.PrimaryCode != "" {
			t.Errorf("PrimaryCode = %q, want no code for a binary file", pkg.PrimaryCode)
		}
	})
}

func TestExtractEvidenceQuotesCallers(t *testing.T) {
	t.Parallel()

	const target = "pkg/target.go"

	fsys := fstest.MapFS{
		target:          {Data: []byte("package pkg\n\nfunc Target() {}\n")},
		"pkg/caller.go": {Data: []byte("package pkg\n\nfunc Caller() {\n\tTarget()\n}\n")},
		"pkg/notes.md":  {Data: []byte("Target() is called from Caller\n")},
	}

	pkg, err := ExtractEvidence(fsys, EvidenceRequest{
		File:      target,
		LineStart: 3,
		LineEnd:   3,
		Title:     "Target ignores its error",
		Body:      "The call `Target()` at line 4 drops the returned error, and Target( is called from elsewhere",
		Evidence:  "Target()",
	})
	if err != nil {
		t.Fatalf("ExtractEvidence() error = %v", err)
	}

	wantStrings(t, pkg.CallerSnippets, []string{
		"pkg/caller.go:4\n1: package pkg\n2: \n3: func Caller() {\n4: \tTarget()\n5: }",
		"pkg/notes.md:1\n1: Target() is called from Caller",
	})
}

func TestExtractEvidenceBoundsItsSearch(t *testing.T) {
	t.Parallel()

	var body strings.Builder
	for index := 1; index <= 10; index++ {
		fmt.Fprintf(&body, "`ident_number_%d` ", index)
	}

	fsys := fstest.MapFS{
		"pkg/eight.go": {Data: []byte("package pkg\n\nfunc eight() {\n\tident_number_8()\n}\n")},
		"pkg/nine.go":  {Data: []byte("package pkg\n\nfunc nine() {\n\tident_number_9()\n}\n")},
	}

	pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "pkg/target.go", LineStart: 1, LineEnd: 1, Title: "t", Body: body.String()})
	if err != nil {
		t.Fatalf("ExtractEvidence() error = %v", err)
	}

	// Ten identifiers are named; the search stops at the eighth, so only the
	// eighth's call site is quoted.
	wantStrings(t, pkg.CallerSnippets, []string{"pkg/eight.go:4\n1: package pkg\n2: \n3: func eight() {\n4: \tident_number_8()\n5: }"})
}

func TestExtractEvidenceCapsCallerSnippets(t *testing.T) {
	t.Parallel()

	var calls strings.Builder
	calls.WriteString("package pkg\n\nfunc many() {\n")
	for range 12 {
		calls.WriteString("\tTarget()\n")
	}
	calls.WriteString("}\n")

	fsys := fstest.MapFS{
		"pkg/target.go": {Data: []byte("package pkg\n\nfunc Target() {}\n")},
		"pkg/many.go":   {Data: []byte(calls.String())},
	}

	pkg, err := ExtractEvidence(fsys, EvidenceRequest{File: "pkg/target.go", LineStart: 3, LineEnd: 3, Title: "t", Body: "Target()"})
	if err != nil {
		t.Fatalf("ExtractEvidence() error = %v", err)
	}

	if len(pkg.CallerSnippets) != 10 {
		t.Fatalf("quoted %d caller snippets, want 10:\n%s", len(pkg.CallerSnippets), strings.Join(pkg.CallerSnippets, "\n---\n"))
	}
	// The call sites are quoted one per matching line, in file order, so the
	// tenth snippet is the tenth call site at line 13 and the last two are
	// dropped.
	if !strings.HasPrefix(pkg.CallerSnippets[0], "pkg/many.go:4\n") {
		t.Errorf("first snippet = %q, want the first call site", pkg.CallerSnippets[0])
	}
	if !strings.HasPrefix(pkg.CallerSnippets[9], "pkg/many.go:13\n") {
		t.Errorf("last snippet = %q, want the tenth call site", pkg.CallerSnippets[9])
	}
}
