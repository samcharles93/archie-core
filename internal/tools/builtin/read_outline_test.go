package builtin

import (
	"fmt"
	"strings"
	"testing"
)

// bigGoFile builds a Go file well past the outline threshold, with each
// function body padded so bodies dominate the byte count.
func bigGoFile(t *testing.T, dir, name string, funcs int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package big\n\nimport \"fmt\"\n\n")
	for i := range funcs {
		fmt.Fprintf(&b, "// Doc for Fn%d.\nfunc Fn%d() {\n", i, i)
		for j := range 20 {
			fmt.Fprintf(&b, "\tfmt.Println(\"body %d line %d\")\n", i, j)
		}
		b.WriteString("}\n\n")
	}
	writeReadTestFile(t, dir, name, b.String())
}

func TestRead_LargeGoFileReturnsOutline(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "big.go", 60)

	res := execRead(t, tmp, `{"path":"big.go"}`)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "func Fn0()") || !strings.Contains(res.Content, "func Fn59()") {
		t.Fatalf("outline should span the whole file, got:\n%s", truncateForMsg(res.Content))
	}
	if strings.Contains(res.Content, "body 0 line 0") {
		t.Fatalf("outline must not include function bodies, got:\n%s", truncateForMsg(res.Content))
	}
	if !strings.Contains(res.Content, "offset") {
		t.Fatalf("outline must tell the model how to read a range, got:\n%s", truncateForMsg(res.Content))
	}
}

// Line numbers are the whole point: without them the outline is not actionable.
func TestRead_OutlineCarriesLineNumbers(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "big.go", 60)

	res := execRead(t, tmp, `{"path":"big.go"}`)
	// Each Fn is preceded by a doc comment, so blocks are 24 lines apart
	// starting at line 6. Assert the mapping is real rather than just present.
	for _, want := range []string{"6: func Fn0()", "30: func Fn1()"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("expected outline entry %q, got:\n%s", want, truncateForMsg(res.Content))
		}
	}
}

func TestRead_ExplicitRangeBypassesOutline(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "big.go", 60)

	res := execRead(t, tmp, `{"path":"big.go","offset":6,"limit":3}`)
	if !strings.Contains(res.Content, "body 0 line 0") {
		t.Fatalf("an explicit range must return raw content, got:\n%s", truncateForMsg(res.Content))
	}
}

func TestRead_FullBypassesOutline(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "big.go", 60)

	res := execRead(t, tmp, `{"path":"big.go","full":true}`)
	if !strings.Contains(res.Content, "body 0 line 0") {
		t.Fatalf("full:true must return raw content, got:\n%s", truncateForMsg(res.Content))
	}
}

// Files at or below the normal read budget must behave exactly as before.
func TestRead_SmallGoFileIsUnaffected(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "small.go", 3)

	res := execRead(t, tmp, `{"path":"small.go"}`)
	if !strings.Contains(res.Content, "body 0 line 0") {
		t.Fatalf("a small file must return raw content, got:\n%s", truncateForMsg(res.Content))
	}
}

// An unrecognised language has no reliable declaration pattern, so outlining
// would risk returning an arbitrary subset of lines as if it were structure.
func TestRead_LargeUnknownFileTypeIsUnaffected(t *testing.T) {
	tmp := t.TempDir()
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "arbitrary line %d\n", i)
	}
	writeReadTestFile(t, tmp, "data.unknownext", b.String())

	res := execRead(t, tmp, `{"path":"data.unknownext"}`)
	if !strings.Contains(res.Content, "arbitrary line 0") {
		t.Fatalf("an unknown file type must fall back to raw content, got:\n%s", truncateForMsg(res.Content))
	}
}

// A large source file with no recognisable declarations (generated tables,
// embedded data) must not collapse to an empty outline.
func TestRead_LargeGoFileWithoutDeclarationsIsUnaffected(t *testing.T) {
	tmp := t.TempDir()
	var b strings.Builder
	b.WriteString("package gen\n")
	for i := range 2000 {
		fmt.Fprintf(&b, "\t\"key%d\": %d,\n", i, i)
	}
	writeReadTestFile(t, tmp, "table.go", b.String())

	res := execRead(t, tmp, `{"path":"table.go"}`)
	if !strings.Contains(res.Content, `"key0"`) {
		t.Fatalf("a file with no declarations must fall back to raw content, got:\n%s", truncateForMsg(res.Content))
	}
}

func truncateForMsg(s string) string {
	if len(s) > 600 {
		return s[:600] + "..."
	}
	return s
}

// An explicit limit that happens to equal the default is still explicit, and
// must be honoured as a content request rather than read as "unspecified".
func TestRead_ExplicitDefaultSizedLimitBypassesOutline(t *testing.T) {
	tmp := t.TempDir()
	bigGoFile(t, tmp, "big.go", 60)

	res := execRead(t, tmp, fmt.Sprintf(`{"path":"big.go","limit":%d}`, DefaultReadLines))
	if !strings.Contains(res.Content, "body 0 line 0") {
		t.Fatalf("an explicit limit must return raw content, got:\n%s", truncateForMsg(res.Content))
	}
}
