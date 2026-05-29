# v0 Uses a Trusted Single-User Permission Baseline

ForgeLane v0 will assume a trusted single-user/self-hosted operator while still
recording privileged actions as ControlActions and Events. This proves the
permission and audit boundary without blocking the first delivery loop on
multi-user RBAC or provider-backed identity.

## Considered Options

- Small-team RBAC in v0: rejected because role design would expand the first
  slice before the issue-to-draft-PR loop is proven.
- Provider-backed identity from day one: rejected because GitHub/GitLab identity
  integration is useful later but not required to prove run control, audit, and
  provider source-of-truth boundaries.

## Consequences

Local operator actions such as `start`, `stop`, `retry`, `request_changes`, and
`close` may execute directly in v0, but they must still create ControlAction and
Event records. Merge automation remains out of scope.
