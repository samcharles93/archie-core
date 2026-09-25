package prreview

import (
	"testing"
	"testing/fstest"
)

// wantStrings compares a sorted string slice against an expectation, treating
// nil and empty as the same answer.
func wantStrings(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBlastRadiusGo(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"go.mod":                       {Data: []byte("module example.com/m\n\ngo 1.27.0\n")},
		"internal/store/store.go":      {Data: []byte("package store\n\nimport \"example.com/m/internal/db\"\n\nfunc Get() int { return db.Value() }\n")},
		"internal/db/db.go":            {Data: []byte("package db\n\nfunc Value() int { return 1 }\n")},
		"internal/app/app.go":          {Data: []byte("package app\n\nimport \"example.com/m/internal/store\"\n\nfunc Run() int { return store.Get() }\n")},
		"internal/store/store_test.go": {Data: []byte("package store\n\nimport \"testing\"\n\nfunc TestGet(t *testing.T) { _ = Get() }\n")},
		"internal/broken/broken.go":    {Data: []byte("package broken\n\nimport (\n")},
		"docs/notes.md":                {Data: []byte("# notes\n")},
	}

	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{
			name:    "the dependent and the dependency are both reached",
			changed: []string{"internal/store/store.go"},
			want:    []string{"internal/app/app.go", "internal/db/db.go"},
		},
		{
			name:    "a changed file is never in its own blast radius",
			changed: []string{"internal/store/store.go", "internal/app/app.go", "internal/db/db.go"},
			want:    nil,
		},
		{
			name:    "an unchanged file that imports nothing internal has no radius",
			changed: []string{"internal/db/db.go"},
			want:    []string{"internal/store/store.go"},
		},
		{
			name:    "a file nothing imports and that imports nothing has no radius",
			changed: []string{"docs/notes.md"},
			want:    nil,
		},
		{
			name:    "a file outside the snapshot contributes nothing",
			changed: []string{"internal/gone/gone.go"},
			want:    nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := BlastRadius(fsys, test.changed)
			if err != nil {
				t.Fatalf("BlastRadius() error = %v", err)
			}
			wantStrings(t, got, test.want)
		})
	}
}

func TestBlastRadiusPython(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"pkg/__init__.py": {Data: []byte("from pkg.util import helper\n")},
		"pkg/util.py":     {Data: []byte("import os\n\n\ndef helper():\n    return os.getcwd()\n")},
		"app.py":          {Data: []byte("from pkg.util import helper\n\nhelper()\n")},
		"cli.py":          {Data: []byte("import pkg\n")},
		"unrelated.py":    {Data: []byte("from missing.module import thing\n")},
	}

	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{
			name:    "a module import reaches the module's file",
			changed: []string{"pkg/util.py"},
			want:    []string{"app.py", "pkg/__init__.py"},
		},
		{
			name:    "a package import reaches the package, and the package's own imports",
			changed: []string{"pkg/__init__.py"},
			want:    []string{"cli.py", "pkg/util.py"},
		},
		{
			name:    "an import this snapshot cannot resolve reaches nothing",
			changed: []string{"missing/module.py"},
			want:    nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := BlastRadius(fsys, test.changed)
			if err != nil {
				t.Fatalf("BlastRadius() error = %v", err)
			}
			wantStrings(t, got, test.want)
		})
	}
}

func TestBlastRadiusJavaScriptAndTypeScript(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"src/a.ts":                  {Data: []byte("export const a = 1\n")},
		"src/b.ts":                  {Data: []byte("import { a } from './a'\n")},
		"src/reexport.ts":           {Data: []byte("export { a } from './a'\n")},
		"src/nested/c.ts":           {Data: []byte("import a from '../a'\n")},
		"src/side.js":               {Data: []byte("require('./a')\n")},
		"src/dyn.ts":                {Data: []byte("export async function load() {\n\treturn import('./a')\n}\n")},
		"src/other.ts":              {Data: []byte("import fs from 'node:fs'\nimport './missing'\n")},
		"src/lib/index.ts":          {Data: []byte("export const lib = 1\n")},
		"src/uses-lib.ts":           {Data: []byte("import './lib'\n")},
		"node_modules/pkg/index.ts": {Data: []byte("import '../../src/a'\n")},
	}

	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{
			name:    "every relative import form reaches the file it names",
			changed: []string{"src/a.ts"},
			want:    []string{"src/b.ts", "src/dyn.ts", "src/nested/c.ts", "src/reexport.ts", "src/side.js"},
		},
		{
			name:    "a directory specifier resolves to its index",
			changed: []string{"src/lib/index.ts"},
			want:    []string{"src/uses-lib.ts"},
		},
		{
			name:    "a bare specifier reaches nothing",
			changed: []string{"src/other.ts"},
			want:    nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := BlastRadius(fsys, test.changed)
			if err != nil {
				t.Fatalf("BlastRadius() error = %v", err)
			}
			wantStrings(t, got, test.want)
		})
	}
}
