# v0 Starts as a Single-Node CLI-First Control Plane

ForgeLane v0 will start with a single-node Go control plane, SQLite persistence,
and a CLI-first control surface. This keeps the first issue-to-draft-PR loop
small enough to prove the core boundary between provider-owned data and
ForgeLane-owned run, event, workspace, and control state before investing in a
web UI, Postgres deployment shape, or separate Rust runner daemon.

## Considered Options

- Web-first control surface: deferred until the delivery loop is observable and
  controllable through the same API from a thinner CLI.
- Postgres-first persistence: deferred because v0 needs local/self-hosted
  operability more than multi-node database behavior.
- Rust runner daemon first: deferred behind a runner boundary so the first loop
  can ship without blocking on sandbox-daemon design.

## Consequences

The v0 implementation should still keep API, store, workflow, provider, runner,
and agent-adapter boundaries explicit so the CLI-first slice does not hard-code
itself into the long-term product shape.
