# Ralph Execution

Ralph is the issue-execution loop for this repo. It consumes agent-ready issues,
implements one narrow slice at a time, verifies it, commits it, and stops with
clear evidence.

This document is tool-agnostic. Any agent may run it by reading this file.
Tool-specific slash commands, schedulers, or headless runners should be thin
adapters that point here.

## Inputs

Before choosing work, read:

- `AGENTS.md`
- `docs/agents/issue-tracker.md`
- `docs/agents/triage-labels.md`
- `docs/agents/domain.md`
- `CONTEXT.md`
- relevant ADRs under `docs/adr/`
- the candidate issue and its parent PRD, if any

Use GitHub as the default issue tracker. Prefer read-only checks before making
tracker changes:

```bash
gh issue list --state open
gh issue view <number> --comments
```

Do not create smoke or test issues while preparing a Ralph run.

## Issue Selection

Pick exactly one issue per iteration.

Allowed work:

- open issue
- labeled `ready-for-agent`
- not blocked by another open issue
- not a PRD parent issue
- acceptance criteria are specific enough to verify

Priority order:

1. Risky integration or architectural foundation work
2. Work that unblocks other ready issues
3. Standard feature slices
4. Cleanup and polish

If the next issue requires a product, security, data ownership, provider API,
sandbox, deployment, or irreversible architecture decision that is not already
captured in the issue or PRD, stop and mark the issue path as HITL instead of
guessing.

## HITL Ralph

Use HITL Ralph before AFK runs, after prompt changes, or for risky work.

Trigger it with the repo convenience script:

```bash
scripts/ralph-once.sh
```

Or for a specific issue:

```bash
scripts/ralph-once.sh --issue 123
```

Select a coding agent explicitly with `--agent <auto|codebuddy|codex|claude>`.
For example, use Codex with:

```bash
scripts/ralph-once.sh --agent codex --issue 123
```

Use Claude Code through the `cc_switch` model wrapper with:

```bash
scripts/ralph-once.sh --agent claude --issue 123
```

The script is only a thin adapter that starts a fresh agent session and points
it at this file. Its default agent is `auto`, which tries CodeBuddy, then Codex,
then Claude. It is not an AFK loop. It creates an isolated worktree by default;
pass `--no-worktree` only when the maintainer explicitly wants to run in the
current checkout.

If the current tool cannot run the script, ask the agent to read this file and
run one HITL iteration:

```text
Read docs/agents/ralph.md and run one HITL Ralph iteration.
```

Or for a specific issue:

```text
Read docs/agents/ralph.md and run one HITL Ralph iteration for issue #123.
```

Before editing files, the agent must stop and report:

- selected issue
- why it is the next issue
- parent PRD, if any
- acceptance criteria
- intended verification commands
- stop condition

After the maintainer confirms, implement only that issue. Stop again before
committing, with a diff summary and verification evidence.

## Branch And Worktree Naming

Ralph-created branches use the `ralph/` namespace:

- `ralph/issue-<number>` for issue-specific runs
- `ralph/once-<timestamp>` for HITL runs without a fixed issue
- `ralph/afk-<timestamp>` for AFK runs without a fixed issue

Ralph-created worktrees live under `.scratch/worktrees/` and use the same suffix
without the `ralph/` prefix. For example, issue `123` uses
`.scratch/worktrees/issue-123` and branch `ralph/issue-123`.

Before creating a new Ralph worktree, the runner fetches the selected base from
`origin` and starts from the refreshed remote-tracking ref when it exists. The
default base follows `origin/HEAD`, falling back to `origin/master` when needed.

Use `--worktree` and `--branch` only when a maintainer needs a deliberate
override. Keep both values in the same vocabulary so worktree names and branch
names remain easy to map.

## Claude Code Model Selection

When Ralph falls back to Claude Code, use `scripts/claude-with-cc-switch.mjs`
instead of invoking `claude` directly. The wrapper reads the same
`~/.models.json` configuration used by `cc_switch`, injects the selected model
environment, and then forwards Ralph's Claude arguments and prompt unchanged.

Use `--agent claude` on `scripts/ralph-once.sh` or `scripts/ralph.sh` to select
that wrapper explicitly. Use `--agent codex` or `--agent codebuddy` for the
other built-in runner profiles without relying on `auto` fallback order.

By default the wrapper uses the `deepseek` model profile when it exists in
`~/.models.json`, falling back to the `cc_switch` default model only when
`deepseek` is not configured. Set `CC_SWITCH_MODEL` when a run needs a specific
model profile.

## AFK Ralph

Use AFK Ralph only after several HITL iterations show that the agent reliably:

- selects the right issue
- avoids PRD parent issues
- stays inside the selected issue scope
- runs feedback loops before commit
- stops on missing human decisions
- writes useful commits

AFK runs must be bounded. Use a small max iteration count, and prefer an
isolated branch, worktree, or sandbox. This repo's AFK runner creates an
isolated worktree by default.

Trigger bounded AFK Ralph with:

```bash
scripts/ralph.sh 5
```

Limit AFK Ralph to a specific issue set with `--issues`. It accepts individual
issue numbers, comma-separated lists, and inclusive ranges:

```bash
scripts/ralph.sh 3 --agent codex --issues 19,20,21
scripts/ralph.sh 5 --issues 19-25
scripts/ralph.sh 5 --issues 19,22-24
```

With one explicit issue, AFK Ralph uses the issue-specific default names:
`issue-<number>` for the worktree and `ralph/issue-<number>` for the branch.

With multiple explicit issues, AFK Ralph runs one bounded loop per issue
concurrently by default. It creates one branch and worktree per issue, using the
normal issue-specific names:

```bash
scripts/ralph.sh 1 --agent codex --issues 19,20,21
```

In parallel mode, `iterations` means the maximum number of agent sessions per
issue. Do not combine parallel issue runs with `--worktree` or `--branch`; those
names are derived from the issue numbers so each agent has an isolated branch.

Use `--serial` only when a maintainer deliberately wants the old scoped batch
behavior: one `ralph/afk-<timestamp>` branch, one worktree, and each iteration
picks at most one issue from the scope.

```bash
scripts/ralph.sh 3 --agent codex --issues 19,20,21 --serial
```

To push the resulting branch and create a GitHub draft pull request after a
successful, unblocked run:

```bash
scripts/ralph.sh 5 --pr
```

This is a separate runner from `scripts/ralph-once.sh`. `ralph-once.sh` is for
HITL observation and stops before editing; `ralph.sh` starts a fresh agent
session per iteration and may implement one eligible issue per iteration.

Each AFK iteration must:

1. Read this file and the progress file.
2. Pick one eligible issue.
3. Implement only that issue.
4. Run relevant verification.
5. Commit only if verification passes.
6. Append concise progress.
7. Stop if no eligible issues remain.

The completion signal is:

```text
COMPLETE
```

Only emit it when no unblocked `ready-for-agent` child issues remain for the
current scope.

## Implementation Method

When the selected issue changes observable behavior and a practical public test
boundary exists, use the TDD workflow before implementation:

1. Write one failing behavior test through a public interface.
2. Implement the smallest change that makes it pass.
3. Refactor only while green.
4. Repeat for the next behavior in the same issue.

Do not use TDD for pure documentation, repository bookkeeping, mechanical
scaffolding, or changes where no practical behavior test boundary exists. For
bugs, first reproduce or diagnose the failure, then add the regression test
before fixing when practical.

## Progress File

Use a temporary progress file during multi-iteration runs:

```text
.scratch/ralph-progress.md
```

Keep entries concise:

- issue completed
- verification commands and results
- files changed
- key decisions
- blockers
- next recommended issue

The progress file is session-specific. Delete it or summarize it back into the
PRD/issues when the run is finished. Durable domain language belongs in
`CONTEXT.md`; durable architecture decisions belong in ADRs.

## Verification

Verification proves the issue is complete; checklist updates only record that
proof.

Pick the smallest command set that proves the issue. While the repository is
mostly documentation, verification may be a focused diff/readthrough plus any
script syntax checks for changed shell scripts:

```bash
git diff --check
bash -n scripts/ralph.sh scripts/ralph-once.sh scripts/lib/*.sh
```

As Go, Rust, and TypeScript packages appear, prefer the smallest relevant
package checks, for example:

```bash
go test ./...
cargo test
pnpm test
pnpm build
```

Do not commit when required verification fails. Fix the issue or stop with the
failure evidence.

## Commits And Pull Requests

Commit after each completed issue or small logical slice. Use the repo Lore
Commit Protocol.

Use `Related:` trailers in commits:

```text
Related: #123
Tested: go test ./...
Not-tested: remote runner integration, deferred to #124
```

Use issue-closing keywords in PR descriptions, not by default in commit
messages:

```text
Closes #123
```

Do not close PRD parent issues automatically. Close parent PRDs only after all
child issues have merged and the PRD has no remaining acceptance criteria.

## Review

Before merge, run a standards/spec review against the target branch and source
issues:

```text
/review main against PRD #<prd> and issues #<issue...>
```

If review finds spec drift, missing acceptance criteria, or scope creep:

- fix implementation in the same branch when the issue was clear;
- update the child issue when only that slice needs clarification;
- update the PRD when the clarification changes multiple slices or the target
  direction;
- update `CONTEXT.md` or ADRs only for durable domain language or architecture
  decisions.

## Tool Adapters

Keep tool-specific commands out of this file. A Codex skill, CodeBuddy slash
command, shell loop, or CI job may invoke this workflow, but this document
should remain the shared source of truth for issue selection, HITL boundaries,
verification, commits, and stop conditions.
