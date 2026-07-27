// Package yaegiutil holds the interpreter-construction and safe-eval
// helpers shared by every Yaegi-interpreted extension surface in
// archie-core (gate scripts, workflow stages, skill plugins, daemon
// plugins, skill scripts). Each surface differs in what symbol tables it
// loads, what export it resolves, and how it invokes that export, but
// all of them build an interpreter the same way and must recover a
// panic from interpreted code  --  including a panic during the call
// itself, not just during eval  --  rather than let it take down the
// daemon.
package yaegiutil

import (
	"fmt"
	"reflect"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

// New returns a Yaegi interpreter with the standard library and any
// extraSymbols loaded. Each interpreted file must get its own
// interpreter when it declares package main  --  Yaegi cannot co-evaluate
// two package-main files with colliding symbols (e.g. two Stage()
// functions) in the same interpreter.
func New(opts interp.Options, extraSymbols ...map[string]map[string]reflect.Value) (*interp.Interpreter, error) {
	i := interp.New(opts)
	if err := i.Use(stdlib.Symbols); err != nil {
		return nil, fmt.Errorf("yaegi: load stdlib symbols: %w", err)
	}
	for _, s := range extraSymbols {
		if err := i.Use(s); err != nil {
			return nil, fmt.Errorf("yaegi: load symbols: %w", err)
		}
	}
	return i, nil
}

// evalTimeout bounds the Yaegi Eval(src) call. Yaegi's Eval is not
// cancellable mid-execution, so a plugin whose package-level init
// blocks forever would hang the daemon without this guard. On timeout
// the goroutine leaks (known limitation), but the daemon continues.
const evalTimeout = 3 * time.Second

// Resolve evaluates src in i and type-asserts exportPath's value
// (e.g. "main.Stage", "gate.Check") to T. It does not itself recover
// panics  --  wrap the call that invokes the returned T in Safe, since a
// panic can happen either during Eval (e.g. an init()) or during the
// caller's later invocation of the resolved function.
//
// The Eval call is guarded by evalTimeout: if package-level init code
// blocks forever (e.g. select{} in init()), Resolve returns an error
// instead of hanging the caller indefinitely. The underlying Yaegi
// goroutine cannot be cancelled and will leak; this is an accepted
// trade-off for daemon availability.
func Resolve[T any](i *interp.Interpreter, src, exportPath string) (result T, err error) {
	done := make(chan error, 1)
	go func() {
		_, evalErr := i.Eval(src)
		done <- evalErr
	}()

	select {
	case evalErr := <-done:
		if evalErr != nil {
			return result, fmt.Errorf("evaluate: %w", evalErr)
		}
	case <-time.After(evalTimeout):
		return result, fmt.Errorf("evaluate: timed out after %v", evalTimeout)
	}

	v, err := i.Eval(exportPath)
	if err != nil {
		return result, fmt.Errorf("does not export %s: %w", exportPath, err)
	}
	fn, ok := v.Interface().(T)
	if !ok {
		return result, fmt.Errorf("%s is %T, want %T", exportPath, v.Interface(), result)
	}
	return fn, nil
}

// Safe runs fn and recovers any panic  --  interpreted code is untrusted
// relative to the daemon's own logic, so a nil dereference or
// out-of-range index inside it must become an error, not a crash.
// label identifies the interpreted source in the resulting error.
func Safe[R any](label string, fn func() (R, error)) (result R, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero R
			result, err = zero, fmt.Errorf("yaegi: %s panicked: %v", label, r)
		}
	}()
	return fn()
}
