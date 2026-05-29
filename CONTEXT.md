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
- Control action: a human action such as stop, retry, request changes, close, or
  reassign.
- Event: an immutable record of something that happened in the delivery loop.
- Repository config: ForgeLane-owned defaults for one target repository, such
  as the default WorkItem provider/repo used to resolve shorthand input; it
  belongs to the local ForgeLane context, not the target source repository or
  provider-owned state.
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
  imports refresh its provider-owned snapshot instead of creating duplicates.
- WorkItem snapshots are cached provider state, not a replacement source of
  truth; show/import output should expose snapshot freshness.
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
