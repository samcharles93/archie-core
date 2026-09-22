package curator

// Definition is a curator definition as data: the declared Manifest plus
// the identity, lifecycle flag, and free-form instructions the manifest
// cannot express. One generic engine (DefinitionEngine) interprets any
// definition -- there is deliberately no catalogue of curator "types" and
// no second authority beside the existing registry.
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
