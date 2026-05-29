# v0 Uses a Generic Command Agent Adapter Boundary

ForgeLane v0 will model the first agent integration as a generic command
adapter boundary, with Codex CLI as the first built-in preset. This keeps the
product from becoming tied to one coding agent while still giving v0 a concrete,
testable execution path.

## Considered Options

- Codex-only adapter: rejected because it would make early domain and API names
  narrower than the control-plane product intent.
- Multiple first-class adapters in v0: rejected because v0 needs one reliable
  issue-to-draft-PR loop before proving breadth across agent runtimes.

## Consequences

The runner contract should describe command execution, workspace scope, logs,
exit status, and produced commits without assuming Codex-specific session
semantics. Codex-specific behavior can live in the preset and later be promoted
only when the core adapter contract proves too weak.
