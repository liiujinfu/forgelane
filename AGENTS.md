# ForgeLane Agent Instructions

These instructions apply to this repository.

## Product Intent

ForgeLane is an open control plane for agentic software delivery.

The product should help teams run coding agents against real engineering work
while keeping the workflow observable, controllable, auditable, and centered on
PR/MR delivery.

## Directional Constraints

- Do not build an independent issue tracker in the early product unless the
  plan explicitly changes. Treat GitHub/GitLab issues as provider-owned source
  data and store only ForgeLane-owned run/control/audit state locally.
- The final deliverable of an agent task is a PR/MR, not a chat answer.
- Keep the early workflow provider-backed: issue -> agent run -> branch ->
  draft PR/MR -> commits -> CI/review -> revise/merge/close.
- Every automated action should be representable as an event.
- Every privileged action should pass through an explicit permission and
  approval boundary.
- Prefer narrow, durable extension points over making the entire workflow
  dynamically mutable.

## Technology Bias

Use this split unless there is a concrete reason to revisit it:

- Go: control plane API, workflow orchestration, provider integrations,
  permissions, event/audit store.
- Rust: runner daemon, sandbox supervision, process isolation, fast local
  execution tooling.
- TypeScript: web app, mobile/PWA surface, VS Code extension, and UI clients.

Avoid introducing new languages or major dependencies before the first working
vertical slice exists.

## Core Domain Boundaries

Keep these concepts stable and explicit:

- WorkItem
- AgentRun
- Workspace
- ChangeSet
- Gate
- Approval
- Event
- Artifact
- ProviderRef

Provider state and ForgeLane state must not be conflated. Provider data may be
cached, but the provider remains the source of truth for issues, PRs/MRs,
reviews, commits, and CI status.

## Extension Boundaries

Prefer interface boundaries for:

- GitProvider
- AgentAdapter
- Runner
- Sandbox
- NotificationChannel
- PolicyEvaluator
- ArtifactStore

Plugins must not bypass event logging, permission checks, or approval gates.

## First Milestone

The first useful skeleton should prove one vertical slice:

1. Import or reference a GitHub/GitLab issue.
2. Start an agent run.
3. Prepare an isolated workspace.
4. Create or attach a draft PR/MR.
5. Stream events and logs.
6. Accept human control actions: approve, request changes, retry, stop, close.

## Documentation

Keep documentation concise and decision-oriented. Record why a boundary exists,
not just what files were added.
