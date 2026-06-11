# ForgeLane

ForgeLane is an open control plane for agentic software delivery.

It is designed for teams that want coding agents to deliver software through
observable, reviewable, and controllable engineering workflows instead of
ending with a chat response.

The first-class deliverable is a pull request or merge request.

## What It Coordinates

ForgeLane does not try to replace GitHub or GitLab in the first version. It
uses them as the source of truth for issues, branches, PRs/MRs, reviews, and
CI status, while ForgeLane owns the agent execution and control layer around
that workflow.

Core concepts:

- Work item: an issue, ticket, or task from a provider such as GitHub or GitLab.
- Agent run: one attempt by a coding agent to move a work item forward.
- Workspace: an isolated checkout and sandbox where the agent works.
- Change set: a branch plus the draft PR/MR produced by the run.
- Gate: review, CI, policy, approval, or request-changes decision.
- Event: an immutable audit record of what happened, when, and why.

## Why

Coding agents are useful, but software delivery needs more than generated code.
Teams need:

- Observable progress: diffs, commits, CI, run logs, and session traces.
- Control points: review comments, request changes, retry, stop, reassign, close.
- Auditability: who approved what, which agent acted, which sandbox produced it.
- Delivery discipline: work flows through PR/MR review, not private chat history.

## v0 Direction

The first version stays intentionally small and is tracked in
[docs/roadmap/v0.md](docs/roadmap/v0.md). The current CLI has the
instance-global ForgeProject configuration path, WorkItem import/cache reads,
planned AgentRun creation, local Workspace preparation, generic command
AgentAdapter execution, log capture, commit materialization, provider-backed
branch push, and draft PR/MR creation for the narrow GitHub and GitLab delivery
paths.

## Architecture Bias

The planned stack is:

- Go for the control plane API, workflow engine, provider integrations, and
  event/audit service. The current v0 CLI is a Go modular monolith.
- Go for the first local Workspace preparation path, behind a runner boundary
  that can later be replaced or supervised by a Rust runner daemon.
- Rust later for sandbox supervision and performance-sensitive execution
  pieces once the first local loop is proven.
- TypeScript for web, mobile/PWA, VS Code extension, and other clients.

The API should be client-neutral so web, mobile, CLI, IDE, and IM integrations
can all observe and control the same underlying workflow.

## Extension Points

ForgeLane should be open without making the core lifecycle unstable.

Likely extension points:

- Git providers: GitHub, GitLab, self-hosted Git.
- Agent adapters: Codex, Claude Code, OpenCode, and local agent runtimes.
- Runners: local machine, single-node server, container pool, cloud runners.
- Notification channels: Slack, Feishu, DingTalk, email, webhooks.
- Policy evaluators: approval rules, risk checks, repo-specific constraints.
- Artifact stores: local filesystem, object storage, provider-native artifacts.

Stable core:

- Work item lifecycle.
- Agent run lifecycle.
- Event log.
- Approval decisions.
- Permission checks.
- PR/MR delivery model.

## Status

This repository is currently in the v0 CLI delivery-loop stage. The CLI exposes
help, version, local repository initialization, WorkItem import/show, planned
AgentRun creation/show, Workspace preparation, agent command execution, log
inspection, ChangeSet delivery, run Event listing, stop, retry,
request-changes, and close. Default tests use fake providers and local git
remotes; real provider mutation is operator-driven and requires explicit
provider tokens.

See [docs/vision.md](docs/vision.md) for the long-term product direction.
See [docs/roadmap/v0.md](docs/roadmap/v0.md) for the first version boundary
and planned milestones.
See [docs/architecture/v0.md](docs/architecture/v0.md) for the first
architecture boundary.
See [docs/testing/v0-smoke.md](docs/testing/v0-smoke.md) for GitHub/GitLab v0
smoke-test steps.

## Local Development

Run the skeleton test suite:

```bash
go test ./...
```

Inspect the current CLI surface:

```bash
go run ./cmd/forgelane --help
go run ./cmd/forgelane version
go run ./cmd/forgelane init --repo-url https://github.com/owner/repo
go run ./cmd/forgelane init --repo-url https://github.com/owner/repo --with-workflow
go run ./cmd/forgelane init --repo-url https://gitlab.com/group/project
go run ./cmd/forgelane init --provider gitlab --repo-url https://gitlab.example.com/group/project.git
go run ./cmd/forgelane workflow init
go run ./cmd/forgelane work-items import github://github.com/owner/repo/issues/123
go run ./cmd/forgelane work-items import gitlab://gitlab.com/group/project/issues/123
go run ./cmd/forgelane work-items import gitlab://gitlab.example.com/group/project/issues/123
go run ./cmd/forgelane work-items show github://github.com/owner/repo/issues/123
go run ./cmd/forgelane work-items show --issue 123
go run ./cmd/forgelane work-items show --id 1
go run ./cmd/forgelane runs create github://github.com/owner/repo/issues/123
go run ./cmd/forgelane runs start github://github.com/owner/repo/issues/123 --agent-preset harmless-echo
go run ./cmd/forgelane runs start gitlab://gitlab.com/group/project/issues/123 --agent-preset harmless-echo
go run ./cmd/forgelane runs show 1
go run ./cmd/forgelane runs prepare 1
go run ./cmd/forgelane runs execute 1
go run ./cmd/forgelane runs request-attention 1 feedback "Which test boundary should I use?"
go run ./cmd/forgelane runs send 1 "Use the CLI boundary."
go run ./cmd/forgelane runs request-attention 1 approval "May I continue with the requested cleanup?"
go run ./cmd/forgelane runs approve 1 approve "Approved for this run."
go run ./cmd/forgelane runs request-changes 1 "Please add focused regression tests."
go run ./cmd/forgelane runs retry 1
go run ./cmd/forgelane runs evidence 1
go run ./cmd/forgelane runs logs 1
go run ./cmd/forgelane events list --run 1
```

Use `runs close <terminal-run-id>` instead of `runs retry` when abandoning the
active local ChangeSet path.

After `runs start`, prefer `forgelane runs evidence <run_id>` as the review and
debugging summary before dropping into raw `runs show`, `runs logs`, or
`events list --run` output.

`work-items import` records a cached provider issue snapshot in the
instance-global SQLite store at `~/.forgelane/forgelane.db` and appends a
compact audit Event. `work-items show` reads only that local snapshot; run
`work-items import` again to refresh provider state explicitly. The default test
suite uses fake WorkItem providers and does not require network access or
credentials.

`init` writes instance config to `~/.forgelane/forgelane.db`, not to the target
repository. When `forgelane.workflow.json` is missing, plain `init` prints the
explicit command to create it but does not modify the repository. Use
`workflow init` or `init --with-workflow` to opt in to the repo-owned workflow
contract. The contract is written at the Git repository root and is
version-controlled repository guidance: default AgentAdapter preset, semantic
tracker label mappings, test command, evidence requirements, approval policy
hints, and automation notes. It must not contain secrets, local paths, run ids,
last errors, or other instance/run state.

`runs create` creates a new planned AgentRun, a succeeded `start`
ControlAction, and an immutable RunSpec snapshot. `runs prepare` allocates a
RunnerJob and Workspace under `~/.forgelane/workspaces/run-<id>/`, clones the
current repository into `repo/`, and records workspace Events. `runs execute`
invokes the configured command AgentAdapter, captures stdout/stderr as log
segments, materializes repository changes into commits, and asks the selected
ChangeProvider to push the ForgeLane-managed branch and create or update the
draft PR/MR. `runs start` performs the create, prepare, execute, materialize,
push, and draft PR/MR path in one command. `events list --run` reads the
AgentRun timeline from SQLite without contacting providers.

When a workflow contract exists, new RunSpecs use its default AgentAdapter
preset unless the operator passes `--agent-preset`. Without a contract, manual
`runs create` and `runs start` continue with built-in defaults and do not check
tracker label eligibility. The default contract documents that future automated
watcher behavior can use the contract's trigger and readiness label mappings;
that watcher is not part of the current CLI delivery loop.

Provider mutation credentials belong to the provider boundary, not the
AgentAdapter process. GitHub delivery reads `FORGELANE_GITHUB_TOKEN` first and
falls back to `GITHUB_TOKEN`; GitLab.com and self-hosted GitLab delivery read
`FORGELANE_GITLAB_TOKEN` first and fall back to `GITLAB_TOKEN`. Self-hosted
GitLab refs keep their host in the canonical `gitlab://host/group/project`
reference, and `init` should use `--provider gitlab` when the repository URL is
not on `gitlab.com`. GitHub tokens need repository-scoped contents write, pull
request write, and issue read capabilities. GitLab tokens need API access for
issues/MRs plus write repository access for Git push. Branch push uses git
transport with a temporary credential helper, not persisted token URLs and not
`gh`/`glab`.

The Run attention loop records pending feedback or approval requests as
AgentRun-targeted ControlActions and Events. `runs show` surfaces pending
attention, `runs send` answers feedback requests, and `runs approve` approves
or rejects approval requests. These user responses enter ForgeLane state rather
than the AgentAdapter process stdin. Provider mutation approval remains a
separate ChangeSet/ChangeProvider boundary.

## Agent Development Workflow

ForgeLane uses repo-local agent guidance and workflow docs:

- [AGENTS.md](AGENTS.md) for repository guardrails.
- [CONTEXT.md](CONTEXT.md) for product vocabulary and source-of-truth
  boundaries.
- [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md) for GitHub issue
  tracking conventions.
- [docs/agents/ralph.md](docs/agents/ralph.md) for the issue-to-PR execution
  loop.
