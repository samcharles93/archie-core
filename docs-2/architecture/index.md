# Architecture

Archie is a personal agent platform built around six domain boundaries:

- Identity tracks users and other actors responsible for actions.
- Agent owns persistent assistants, specialisation, and continuity.
- Messaging owns conversations, messages, branches, and channel correlation.
- Workflow owns reusable, versioned behaviour definitions and their executions.
- Work Intake admits optional durable work without constraining ordinary agent
  conversation or direct capability use.
- Plugin owns generic extension discovery and lifecycle; each capability family
  owns its typed plugin contract.

The detailed architecture and migration decisions are maintained under
`docs/prds/architecture/` in the repository while this generated site is being
assembled.
