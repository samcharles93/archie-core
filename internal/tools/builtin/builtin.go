package builtin

// RegisterBuiltins registers all built-in tools into the given registry.
// The cwd parameter sets the working directory for file and shell operations.
func RegisterBuiltins(reg *Registry, cwd string) error {
	mq := NewMutationQueue()
	rt := NewReadTracker()

	builtins := []Tool{
		NewReadTool(cwd, rt),
		NewWriteTool(cwd, mq, rt),
		NewEditTool(cwd, mq, rt),
		NewShellTool(cwd, mq),
		NewGrepTool(cwd),
		NewFindTool(cwd),
	}

	for _, tool := range builtins {
		if err := reg.Register(tool); err != nil {
			return err
		}
	}

	return nil
}
