# Vision

ForgeLane is an open control panel for agent-driven software delivery.

Its goal is not to make coding agents smarter in isolation. Its goal is to make
agent work fit the way engineering teams actually ship software: issues define
intent, isolated runs execute work, draft PRs expose progress, CI and review
provide checks, and humans can observe, interrupt, redirect, approve, or reject
the work.

ForgeLane treats existing Git providers and CI systems as the delivery
substrate, then adds the missing control layer around coding agents.

## North Star

Agentic software delivery should move from private agent conversations to team
delivery loops that are observable, controllable, and deliverable.

A user should be able to start from an issue, delegate implementation to an
agent, watch the work unfold, intervene when needed, and receive a PR/MR that
can be reviewed, tested, revised, approved, or closed through normal engineering
controls.

That means:

- Observable: diffs, commits, CI, run logs, session logs, and events are visible
  while work is happening.
- Controllable: humans can stop, retry, request changes, reassign, approve, or
  close the work.
- Deliverable: the final unit of work is a PR/MR, not a chat answer.

## What Changes

Today, coding agents often behave like powerful chat sessions: useful for
producing code, but weak as a team delivery mechanism.

ForgeLane treats the agent as an execution participant inside the existing
software delivery system:

- the issue defines intent;
- the run records execution;
- the branch and PR/MR expose the change;
- CI and review provide independent checks;
- human control actions can stop, retry, redirect, or reject the work;
- the event log preserves what happened and why.

The shift is from "agent answered" to "agent delivered a reviewable change."

## Product Shape

ForgeLane should eventually provide:

- a provider-backed work queue over GitHub, GitLab, and similar systems;
- cloud or local agent execution with isolated workspaces;
- draft PR/MR creation early in the run, not only at the end;
- live run status, session logs, commits, diffs, CI state, and review state;
- human controls such as stop, retry, request changes, reassign, close, and
  approve;
- approval gates for privileged or risky actions;
- an auditable event history for automated and human actions;
- client surfaces for web, mobile/PWA, CLI, IDE, and IM integrations;
- extension points for providers, runners, agents, policies, notifications,
  and artifact storage.

## What ForgeLane Is Not

ForgeLane is not:

- a replacement for GitHub, GitLab, or other Git providers;
- a replacement for CI;
- an independent issue tracker by default;
- a single coding agent;
- a chatbot interface for code generation;
- a generic workflow automation engine with agents attached;
- a hidden execution service that bypasses review, approval, or audit.

ForgeLane should sit above existing delivery tools and make agent execution
observable and governable.

## Long-Term Principles

- Open: core workflows and interfaces should be inspectable and extensible.
- Self-hostable: teams should be able to run ForgeLane on their own
  infrastructure.
- Provider-backed: external issue, PR/MR, review, commit, and CI systems remain
  the source of truth for their own data.
- PR/MR-centered: the primary deliverable is a reviewable change, not a chat
  transcript.
- Observable: progress should be visible through logs, events, commits, diffs,
  CI, and reviews.
- Controllable: humans need clear control actions during and after agent runs.
- Auditable: automated and privileged actions need durable records.
- Extensible with guardrails: providers, runners, agents, notifications,
  policies, and artifact storage can vary, but they must not bypass event logs,
  permissions, or approval gates.

## Success State

ForgeLane succeeds when a team can trust coding agents with real work because
the work remains inside the team's normal delivery discipline.

The team should not have to choose between agent autonomy and engineering
control. ForgeLane should let agents move work forward while preserving the
review, approval, and audit practices that make software delivery safe.
