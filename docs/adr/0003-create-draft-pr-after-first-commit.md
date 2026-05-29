# v0 Creates Draft PRs After the First Pushed Commit

ForgeLane v0 will create a draft PR after an AgentRun has produced and pushed
its first commit, not at run start and not only after terminal completion. This
keeps progress visible early while avoiding empty draft PRs for runs that fail
before creating a reviewable change.

## Considered Options

- Create the draft PR at run start: rejected because it creates provider noise
  before ForgeLane has a branch with a meaningful diff.
- Create the draft PR only after terminal success: rejected because reviewers
  lose early visibility into long-running or partially successful work.

## Consequences

Runs that fail before a commit remains visible through AgentRun events and logs
without creating a ChangeSet PR. Runs that push commits but fail to open a draft
PR can leave the ChangeSet at `branch_ready` and retry PR creation without
reusing the failed AgentRun.
