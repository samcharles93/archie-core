// Lifted from tau internal/agent/tools/builtin.go
// tau commit f5289ea3782c099339c2d26fe3af8ebcf42ba52d (2026-07-27).
//
// Mutations from upstream:
//   - package renamed tools -> builtin (archie-core already has an
//     internal/tools package holding the registry these are registered into).
//   - the docs tool is not registered and docs.go was not lifted. It serves
//     tau's own embedded documentation via the tau/docs package, which has
//     no meaning here. The pluginDocs parameter went with it.
//
// Refresh by diffing against that path at a newer tau commit. Do not
// edit without recording the change above.
package builtin

// RegisterBuiltins registers all built-in tools into the given registry.
// The cwd parameter sets the working directory for file and shell operations.
func RegisterBuiltins(reg *Registry, cwd string, indexes ...GrepIndex) error {
	mq := NewMutationQueue()
	rt := NewReadTracker()

	builtins := []Tool{
		NewReadTool(cwd, rt),
		NewWriteTool(cwd, mq, rt),
		NewEditTool(cwd, mq, rt),
		NewShellTool(cwd, mq),
		NewGrepTool(cwd, indexes...),
		NewFindTool(cwd),
	}

	for _, tool := range builtins {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}

	return nil
}
