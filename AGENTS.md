# ForgeLane Agent Instructions

ForgeLane is an open control plane for agentic software delivery. Keep agent
workflows observable, controllable, auditable, and centered on PR/MR delivery.

## Product Guardrails

- Do not build an independent issue tracker in the early product unless the
  plan explicitly changes.
- Treat GitHub/GitLab issues, PRs/MRs, reviews, commits, and CI status as
  provider-owned source data. Store only ForgeLane-owned run, control, audit,
  and cached reference state locally.
- The deliverable for an agent task is a PR/MR, not a chat answer.
- Keep the early workflow provider-backed: issue -> agent run -> branch ->
  draft PR/MR -> commits -> CI/review -> revise/merge/close.
- Every automated action should be representable as an event.
- Every privileged action should pass through an explicit permission or
  approval boundary.
- Prefer narrow extension points over making the whole workflow dynamically
  mutable.

## Technology Guardrails

Use this split unless there is a concrete reason to revisit it:

- Go: control plane API, workflow orchestration, provider integrations,
  permissions, event/audit store.
- Rust: runner daemon, sandbox supervision, process isolation, fast local
  execution tooling.
- TypeScript: web app, mobile/PWA surface, VS Code extension, and UI clients.

Avoid new languages, frameworks, or major dependencies before the first working
vertical slice exists.

## Extension Guardrails

Provider, runner, agent, notification, policy, and artifact integrations may be
pluggable over time. Plugins must not bypass event logging, permission checks,
approval gates, or provider source-of-truth boundaries.

## Documentation

Keep documentation concise and decision-oriented. Record why a boundary exists,
not just what files were added.
