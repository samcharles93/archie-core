package messaging

// localCommands is the executable gateway-local command list. It lives here
// beside CommandSpec so adapter-published help surfaces cannot drift from
// the executable router surface.
var localCommands = []string{
	"/status", "/tasks", "/model", "/spawn",
	"/approve", "/cancel", "/start",
	"/new", "/reset", "/topic", "/retry", "/undo",
	"/title", "/branch", "/fork", "/compress", "/compact",
	"/whoami", "/profile", "/sessions", "/resume", "/delete", "/agents",
	"/personality", "/help", "/version", "/update", "/restart",
}

// CommandSpec describes a local command for adapter-provided discovery.
type CommandSpec struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

var localCommandSpecs = []CommandSpec{
	{Command: "/status", Description: "Show daemon health", Usage: "/status"},
	{Command: "/tasks", Description: "Show running and parked work", Usage: "/tasks"},
	{Command: "/model", Description: "Choose a provider and model", Usage: "/model [provider/model]"},
	{Command: "/spawn", Description: "Create a tracked task", Usage: "/spawn <title>"},
	{Command: "/approve", Description: "Approve a waiting task", Usage: "/approve <task-id>"},
	{Command: "/cancel", Description: "Cancel a queued or waiting task", Usage: "/cancel <task-id>"},
	{Command: "/start", Description: "Confirm that Archie is running", Usage: "/start"},
	{Command: "/new", Description: "Start a fresh conversation", Usage: "/new [title]"},
	{Command: "/reset", Description: "Alias for /new", Usage: "/reset [title]"},
	{Command: "/topic", Description: "List or switch conversation topics", Usage: "/topic [session-id]"},
	{Command: "/retry", Description: "Replay the last message", Usage: "/retry"},
	{Command: "/undo", Description: "Remove recent messages", Usage: "/undo [N]"},
	{Command: "/title", Description: "Show or set the conversation title", Usage: "/title [name]"},
	{Command: "/branch", Description: "Create a child conversation", Usage: "/branch [name]"},
	{Command: "/fork", Description: "Alias for /branch", Usage: "/fork [name]"},
	{Command: "/compress", Description: "Compress conversation context", Usage: "/compress [options]"},
	{Command: "/compact", Description: "Alias for /compress", Usage: "/compact [options]"},
	{Command: "/whoami", Description: "Show the active identity and model", Usage: "/whoami"},
	{Command: "/profile", Description: "Show the active identity profile", Usage: "/profile"},
	{Command: "/sessions", Description: "List this channel's conversations", Usage: "/sessions"},
	{Command: "/resume", Description: "Switch to a conversation", Usage: "/resume <session-id>"},
	{Command: "/delete", Description: "Delete a conversation and its history", Usage: "/delete <session-id>"},
	{Command: "/agents", Description: "List tasks currently being worked", Usage: "/agents"},
	{Command: "/personality", Description: "Choose a communication style", Usage: "/personality [name]"},
	{Command: "/help", Description: "See what Archie can do", Usage: "/help"},
	{Command: "/version", Description: "Show installed Archie versions", Usage: "/version"},
	{Command: "/update", Description: "Check for Archie updates", Usage: "/update"},
	{Command: "/restart", Description: "Reload the chat adapter", Usage: "/restart"},
}

// LocalCommands returns the command names Route answers from local state.
// Gateways use this to verify that their published command surfaces match
// the executable router surface.
func LocalCommands() []string {
	return append([]string(nil), localCommands...)
}

// LocalCommandSpecs returns a copy safe for adapters to enrich with their
// optional capabilities.
func LocalCommandSpecs() []CommandSpec {
	return append([]CommandSpec(nil), localCommandSpecs...)
}
