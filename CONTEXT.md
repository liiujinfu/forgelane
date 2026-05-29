# ForgeLane Context

ForgeLane is an agentic software delivery control plane.

## Product Vocabulary

- Work item: a provider-owned issue, ticket, or task that starts delivery work.
- Agent run: one bounded attempt by a coding agent to move a work item forward.
- Workspace: an isolated checkout and execution environment for an agent run.
- Change set: the branch and draft PR produced or updated by an agent run.
- Agent adapter: the boundary that invokes a specific coding agent or command
  from a run spec.
- Control action: a human action such as stop, retry, request changes, close, or
  reassign.
- Event: an immutable record of something that happened in the delivery loop.
- Provider reference: a stable reference to provider-owned data such as a GitHub
  issue, PR, commit, review, or CI status.

## Invariants

- Provider-owned data and ForgeLane-owned state must stay distinct.
- GitHub/GitLab remain the source of truth for issues, PRs/MRs, reviews,
  commits, and CI status.
- ForgeLane owns run state, control actions, approvals, events, logs,
  workspaces, and artifacts.
- The first-class deliverable is a PR/MR, not a chat answer.
- Automated and privileged actions must be auditable through events.
- Privileged actions must pass through an explicit permission or approval
  boundary.

## Early Product Boundaries

- v0 targets GitHub first.
- v0 should prove one issue-to-draft-PR loop before adding provider breadth.
- v0 assumes a trusted single-user/self-hosted operator while still recording
  privileged actions through ControlAction and Event records.
- Do not introduce an independent issue tracker unless the roadmap changes.
- Do not add a plugin system before a working vertical slice exists.
- Do not make cloud runner assumptions that prevent a local/single-node runner.

## Naming Guidance

Use `AgentRun`, `Workspace`, `ChangeSet`, `ControlAction`, `Event`, and
`ProviderRef` consistently in docs and code until a later architecture decision
renames them. Use `AgentAdapter` for the integration boundary and reserve
agent-specific names such as Codex CLI for adapter presets or configuration.
