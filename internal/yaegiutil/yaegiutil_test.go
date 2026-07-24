package yaegiutil_test

import (
	"strings"
	"testing"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

func TestResolve(t *testing.T) {
	i, err := yaegiutil.New(interp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	fn, err := yaegiutil.Resolve[func() string](i, `package main
func Greet() string { return "hi" }`, "main.Greet")
	if err != nil {
		t.Fatal(err)
	}
	if got := fn(); got != "hi" {
		t.Errorf("got %q, want %q", got, "hi")
	}
}

func TestResolveWrongType(t *testing.T) {
	i, err := yaegiutil.New(interp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = yaegiutil.Resolve[func() int](i, `package main
func Greet() string { return "hi" }`, "main.Greet")
	if err == nil {
		t.Fatal("want error for wrong type, got nil")
	}
}

func TestResolveMissingExport(t *testing.T) {
	i, err := yaegiutil.New(interp.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = yaegiutil.Resolve[func() string](i, `package main
func Other() string { return "hi" }`, "main.Greet")
	if err == nil {
		t.Fatal("want error for missing export, got nil")
	}
}

func TestSafeRecoversPanic(t *testing.T) {
	_, err := yaegiutil.Safe("test", func() (string, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("want error from recovered panic, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error %q does not mention panic value", err.Error())
	}
}

func TestSafePassesThroughResult(t *testing.T) {
	got, err := yaegiutil.Safe("test", func() (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}
}
