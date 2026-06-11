# v0 Smoke Testing

Use this checklist to verify the v0 issue-to-draft-PR loop against a real
provider while keeping provider mutations explicit.

## Build

```bash
go test ./...
go build -o /tmp/forgelane-v0-smoke ./cmd/forgelane
```

Use a disposable ForgeLane state directory for every smoke run:

```bash
export HOME="$(mktemp -d /tmp/forgelane-smoke.XXXXXX)"
```

Run smoke tests from a clean checkout of the target repository. The checkout
remote must match the provider issue repository.

## Non-Mutating GitHub Smoke

This path imports the issue, prepares a workspace, runs the harmless adapter,
and verifies evidence. It should not create a branch or draft PR because the
adapter makes no repository changes.

```bash
/tmp/forgelane-v0-smoke init --repo-url https://github.com/OWNER/REPO.git
/tmp/forgelane-v0-smoke work-items import github://github.com/OWNER/REPO/issues/ISSUE
/tmp/forgelane-v0-smoke runs start github://github.com/OWNER/REPO/issues/ISSUE --agent-preset harmless-echo
/tmp/forgelane-v0-smoke runs evidence 1
/tmp/forgelane-v0-smoke runs logs 1
/tmp/forgelane-v0-smoke events list --run 1
```

Expected evidence:

- AgentRun status is `completed`.
- Delivery says no repository changes were produced.
- No ChangeSet, branch, or draft PR is created.
- Logs are readable and do not include provider token material.

## Non-Mutating GitLab Smoke

Use GitLab refs for GitLab.com and include the host for self-hosted GitLab.
Self-hosted repositories should pass `--provider gitlab` during init.

```bash
/tmp/forgelane-v0-smoke init --provider gitlab --repo-url https://gitlab.example.com/GROUP/PROJECT.git
/tmp/forgelane-v0-smoke work-items import gitlab://gitlab.example.com/GROUP/PROJECT/issues/ISSUE
/tmp/forgelane-v0-smoke runs start gitlab://gitlab.example.com/GROUP/PROJECT/issues/ISSUE --agent-preset harmless-echo
/tmp/forgelane-v0-smoke runs evidence 1
/tmp/forgelane-v0-smoke runs logs 1
/tmp/forgelane-v0-smoke events list --run 1
```

Expected evidence matches the GitHub non-mutating smoke.

## Provider-Write Smoke

Run this only against a disposable issue/repository or with an explicit cleanup
plan. This path creates or updates a provider branch and draft PR/MR.

Set one provider token in the shell running ForgeLane. Do not pass tokens to the
AgentAdapter process.

```bash
export FORGELANE_GITHUB_TOKEN="..."
# or
export FORGELANE_GITLAB_TOKEN="..."
```

Create and prepare a run, then add a tiny file in the prepared workspace before
executing:

```bash
/tmp/forgelane-v0-smoke runs create PROVIDER_REF --agent-preset harmless-echo
/tmp/forgelane-v0-smoke runs prepare 1
printf 'ForgeLane v0 smoke\n' > "$HOME/.forgelane/workspaces/run-1/repo/forgelane-smoke.txt"
/tmp/forgelane-v0-smoke runs execute 1
/tmp/forgelane-v0-smoke runs evidence 1
```

Expected evidence:

- AgentRun status is `completed`.
- A local commit was materialized.
- ChangeSet status reaches `draft_open`, or stays in a recoverable
  provider-failure status with retry guidance.
- Provider branch and draft PR/MR refs are shown when delivery succeeds.
- Events include `repository_commit.materialized`, `change_set.created`,
  branch push events, and draft PR/MR events.

## Control Smoke

After a terminal run with an active ChangeSet:

```bash
/tmp/forgelane-v0-smoke runs request-changes 1 "Please address the smoke review note."
/tmp/forgelane-v0-smoke runs retry 1
```

For an abandoned local delivery path, run close before starting a retry, or
after the follow-up run is terminal:

```bash
/tmp/forgelane-v0-smoke runs close 1 "Close the local delivery path."
```

Expected evidence:

- `request-changes` records a succeeded ControlAction and a
  `change_set.changes_requested` event.
- `retry` creates a fresh planned AgentRun and targets the existing active
  ChangeSet while it is `changes_requested`.
- `close` records a succeeded ControlAction and a `change_set.closed` event.
- Closed ChangeSets remain inspectable in local evidence but are not reused as
  active retry targets.
