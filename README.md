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

## MVP Direction

The first version should stay intentionally small:

1. Connect to GitHub or GitLab as the external work tracker.
2. Start an agent run from an issue.
3. Create an early draft PR/MR and keep pushing commits to it.
4. Stream run status, logs, commits, CI, and review state into ForgeLane.
5. Allow a human to approve, request changes, retry, stop, or close the run.

## Architecture Bias

The planned stack is:

- Go for the control plane API, workflow engine, provider integrations, and
  event/audit service.
- Rust for the local runner, sandbox supervisor, and performance-sensitive
  execution pieces.
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

This repository is currently at the naming and project-skeleton stage.

See [ROADMAP.md](ROADMAP.md) for the first version boundary and planned
milestones.

## Agent Development Workflow

ForgeLane uses repo-local agent guidance and workflow docs:

- [AGENTS.md](AGENTS.md) for repository guardrails.
- [CONTEXT.md](CONTEXT.md) for product vocabulary and source-of-truth
  boundaries.
- [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md) for GitHub issue
  tracking conventions.
- [docs/agents/ralph.md](docs/agents/ralph.md) for the issue-to-PR execution
  loop.
