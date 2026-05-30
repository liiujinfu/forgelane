# v0 Uses an Instance-Global SQLite State Store

ForgeLane v0 will store ForgeProjects, WorkItem snapshots, Events, and later run
state in one local instance-global SQLite database at
`~/.forgelane/forgelane.db`, not in `.forgelane/` directories inside target
source repositories. The current working directory may still help infer a
default ForgeProject from a Git remote, but one ForgeLane instance should be
able to manage multiple provider-backed projects and keep control-plane state
out of target repositories.

## Considered Options

- Repo-local `.forgelane/` state: rejected because it makes multi-project
  control-plane views awkward and puts ForgeLane-owned state inside target
  source repository directories.
- Instance-global SQLite state: accepted because it matches the control-plane
  model, keeps audit and WorkItem state queryable across projects, and still
  stays simple for v0's single-user self-hosted baseline.
