# Roadmap

This roadmap defines product boundaries for ForgeLane. It is not a task list.
Implementation work should be tracked in GitHub issues and linked back to the
milestone sections here.

## Roadmap Principles

- Ship one observable delivery loop before adding breadth.
- Use GitHub/GitLab as the provider source of truth for work items, branches,
  PRs/MRs, reviews, commits, and CI.
- Store ForgeLane-owned state for runs, control actions, approvals, events,
  logs, workspaces, and artifacts.
- Make PR/MR delivery the unit of completion.
- Keep extension points narrow until the first vertical slice works end to end.

## v0: Single Issue to Draft PR Loop

### Goal

Start from one GitHub issue, run one coding agent in an isolated workspace,
create or update a draft PR, and expose enough events and controls for a human
to inspect, stop, retry, or request changes.

### User Outcome

A user can point ForgeLane at a GitHub issue and watch the agent turn that issue
into a draft PR with commits, logs, status, and a basic control surface.

### In Scope

- GitHub as the first provider.
- Repository and issue reference import.
- AgentRun lifecycle with explicit states.
- Local or single-node workspace preparation.
- One agent adapter path.
- Branch creation and draft PR creation.
- Event log for run lifecycle, agent actions, commits, PR updates, and control
  actions.
- Session log capture for the agent run.
- Basic run detail view or CLI output showing status, events, logs, branch, and
  PR link.
- Human control actions: stop, retry, request changes, and close.

### Out of Scope

- Independent issue tracker.
- Multi-provider support beyond the first GitHub path.
- Multi-agent planning or worker orchestration.
- Mobile app, VS Code extension, or IM integration.
- Plugin marketplace.
- Multi-tenant organization management.
- Cloud autoscaling runner fleet.
- Advanced policy engine.
- Full CI replacement.

### Acceptance Criteria

- A GitHub issue can be referenced as the starting work item.
- ForgeLane can create an AgentRun for that issue.
- A workspace is prepared for the target repository.
- The agent can make a commit on a branch.
- A draft PR is created or updated for the branch.
- Run events and agent logs are visible from ForgeLane.
- A human can stop a running AgentRun.
- A human can retry or request changes after a run finishes or fails.
- Provider-owned data and ForgeLane-owned run state are stored separately.

## v0.1: Provider-Backed Review Loop

### Goal

Close the loop between PR review feedback and follow-up agent runs.

### Likely Scope

- Read PR review comments and requested changes from GitHub.
- Start a follow-up AgentRun from review feedback.
- Append new commits to the existing PR branch.
- Record the relationship between review feedback and retry attempts.
- Surface CI status alongside run status.

## v0.2: Usable Control Surface

### Goal

Make ForgeLane useful for repeated daily use by a small team.

### Likely Scope

- Web run list and run detail pages.
- Basic authentication for a self-hosted instance.
- Repository-level configuration.
- Notification webhooks for run status and approval events.
- Minimal audit trail export.

## Later Directions

- GitLab provider support.
- Multiple agent adapters.
- Runner pools and remote execution.
- Mobile/PWA review surface.
- VS Code extension.
- IM integrations.
- Policy plugins.
- Artifact storage backends.
- Organization/team permissions.

## Open Questions

- Should the first agent adapter target Codex CLI, Claude Code, OpenCode, or a
  generic command adapter?
- Should the first runner be Go-only, Rust-backed, or a simple process runner
  behind a future Rust boundary?
- Should the first UI be web-first, CLI-first, or both with the same API?
- What is the minimal persistent store for v0: SQLite, Postgres, or an embedded
  event log?
