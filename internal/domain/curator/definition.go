package curator

// Definition is a curator defined as data, run by DefinitionEngine.
type Definition struct {
	// Name is the registry key.
	Name string
	// Enabled gates registration: a disabled definition is seed data that
	// the app layer does not register.
	Enabled bool
	// Instructions is the free-form system prompt for the generic engine.
	Instructions string
	// Manifest is the declared shape, verbatim.
	Manifest Manifest
}
