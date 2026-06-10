# ForgeLane Context

ForgeLane is an agentic software delivery control plane.

## Product Vocabulary

- Work item: a provider-owned issue, ticket, or task that starts delivery work.
- Work item import: a pre-run operation that records a provider-owned work item
  reference and snapshot before any AgentRun exists.
- Agent run: one bounded attempt by a coding agent to move a work item forward.
- Workspace: an isolated checkout and execution environment for an agent run.
- Change set: the branch and draft PR produced or updated by an agent run.
- Agent adapter: the boundary that invokes a specific coding agent or command
  from a run spec.
- Run attention loop: ForgeLane-owned interaction state that lets an AgentRun
  wait for user feedback or approval through ControlAction/Event history instead
  of interactive AgentAdapter process input.
- Run attention request: a pending AgentRun-targeted ControlAction asking for
  feedback or approval; it is resolved by user feedback, approval, or rejection.
- Change provider: the boundary that mutates provider-owned delivery artifacts
  such as branches and PRs after ForgeLane records the matching ControlAction.
- Control action: a human action such as stop, retry, request changes, close, or
  reassign.
- Event: an immutable record of something that happened in the delivery loop.
- Repository config: ForgeLane-owned defaults for one target repository or
  ForgeProject, such as the default WorkItem provider/repo used to resolve
  shorthand input.
- Instance state store: ForgeLane-owned persistent state for one local
  ForgeLane instance, including ForgeProjects, WorkItem snapshots, and Events.
- Target repository: the Git repository ForgeLane will prepare as the code
  workspace and later deliver changes against.
- Default WorkItem source: the provider-owned source used to resolve shorthand
  WorkItem input when the user does not provide a full ProviderRef.
- Forge project: a provider-hosted project such as a GitHub repository or
  GitLab project that can imply both a TargetRepository and a DefaultWorkItemSource.
- Provider reference: a canonical stable reference to provider-owned data such
  as `github://github.com/owner/repo/issues/123`, a PR, commit, review, or CI
  status; canonical ProviderRefs include the provider instance host.

## Invariants

- Provider-owned data and ForgeLane-owned state must stay distinct.
- GitHub/GitLab remain the source of truth for issues, PRs/MRs, reviews,
  commits, and CI status.
- Importing a WorkItem snapshot does not imply starting an AgentRun.
- CLI shorthand such as issue number `123` must resolve to a canonical
  ProviderRef before workflow, persistence, Event, or audit boundaries.
- Repository auto-detection should be transparent and overridable, not
  interactive; CLI output may report detected defaults, but scripts and tests
  must not block on prompts.
- Repository initialization may accept public-forge shorthand such as
  `--provider github --repo owner/repo`, but this is a CLI convenience over a
  canonical repository URL and must not become the persisted identity.
- A canonical WorkItem ProviderRef identifies one ForgeLane WorkItem; repeated
  imports refresh its provider-owned snapshot and append audit Events instead
  of creating duplicate WorkItems, even when provider content has not changed.
- Provider pull requests and merge requests are delivery artifacts or later
  review/fix inputs, not issue WorkItems in the v0 WorkItem import path.
- ForgeLane instance state such as ForgeProjects, WorkItem snapshots, and
  Events belongs in the instance state store, not inside target source
  repositories and not in provider-owned systems.
- WorkItem snapshots are cached provider state, not a replacement source of
  truth; show/import output should expose snapshot freshness.
- WorkItem `imported_at` records the first local import time; `refreshed_at`
  records the most recent explicit import/refresh time.
- WorkItem show commands read cached ForgeLane snapshots. Provider refresh is
  an explicit import operation, not an implicit side effect of viewing.
- Provider identity is the primary CLI lookup path for WorkItems. Local
  WorkItem ids may exist for storage, joins, and explicit debugging commands,
  but they are not the main user-facing identity.
- A TargetRepository and DefaultWorkItemSource often point at the same
  GitHub/GitLab project, but they are distinct concepts and may diverge.
- A ForgeProject is the preferred config shape when one GitHub/GitLab project
  supplies both the TargetRepository and DefaultWorkItemSource.
- A plain Git TargetRepository does not imply a DefaultWorkItemSource; numeric
  WorkItem shorthand is unavailable until a WorkItem provider source is
  configured.
- When CLI input explicitly names a WorkItem provider, ForgeLane must either
  configure a matching DefaultWorkItemSource or fail clearly instead of silently
  degrading to TargetRepository-only config.
- ForgeLane owns run state, control actions, approvals, events, logs,
  workspaces, and artifacts.
- Run attention feedback and approval are ForgeLane-owned AgentRun state. User
  responses must enter through ControlAction/Event records, not by attaching
  stdin or hidden interaction channels to the AgentAdapter process.
- Run attention approval is distinct from provider mutation approval or
  ChangeSet approval. Provider branch and PR/MR mutations remain ChangeProvider
  responsibilities after ForgeLane records the matching ControlAction boundary.
- AgentRun and RunnerJob command execution use explicit runtime states:
  `running`, `completed`, `failed`, `timed_out`, and `cancelled`. Runtime
  `cancelled` means the executing command context was cancelled; it does not by
  itself imply that a user-facing stop ControlAction exists or was approved.
- Command terminal Events distinguish success, failure, timeout, and runtime
  cancellation through `agent_command.completed`, `agent_command.failed`,
  `agent_command.timed_out`, and `agent_command.cancelled`.
- No-change delivery is an explicit successful terminal result: when an
  AgentAdapter completes and `repo/` has no tracked or untracked changes,
  ForgeLane records `repository_delivery.skipped` with reason
  `no_repository_changes` and does not create commit refs, a ChangeSet, branch
  push, or draft PR.
- The first-class deliverable is a PR/MR, not a chat answer.
- Automated and privileged actions must be auditable through events.
- Privileged actions must pass through an explicit permission or approval
  boundary.
- Provider mutation credentials belong to the ChangeProvider boundary, not the
  AgentAdapter command environment.

## Early Product Boundaries

- v0 targets GitHub first. Issue #40 adds only a narrow GitLab.com and
  self-hosted GitLab path for the same WorkItem import plus branch-to-draft-MR
  ChangeProvider loop; this does not open general provider breadth.
- v0 should prove one issue-to-draft-PR/MR loop before adding broader provider
  breadth, including checks, reviews, comments, provider webhooks, or a plugin
  system.
- v0 assumes a trusted single-user/self-hosted operator while still recording
  privileged actions through ControlAction and Event records.
- Do not introduce an independent issue tracker unless the roadmap changes.
- Do not add a plugin system before a working vertical slice exists.
- Do not make cloud runner assumptions that prevent a local/single-node runner.

## Naming Guidance

Use `AgentRun`, `Workspace`, `ChangeSet`, `ChangeProvider`, `ControlAction`,
`Event`, and `ProviderRef` consistently in docs and code until a later
architecture decision renames them. Use `AgentAdapter` for the integration
boundary and reserve agent-specific names such as Codex CLI for adapter presets
or configuration.
