# v0 Adds a Narrow GitLab Delivery Slice

ForgeLane will keep GitHub as the first v0 provider while also supporting a
narrow GitLab.com and self-hosted GitLab delivery slice: WorkItem import,
branch push, and draft merge request create/update through the existing
ProviderRef and ChangeProvider boundaries.

This is not a general multi-provider expansion. GitLab support must stay
host-aware, use canonical refs such as
`gitlab://gitlab.example.com/group/project/issues/123`, and keep provider-owned
issues, branches, merge requests, reviews, commits, and CI status outside
ForgeLane-owned state.

## Considered Options

- Keep v0 GitHub-only: rejected because host-qualified ProviderRefs already
  model provider instances, and self-hosted GitLab is a common early operator
  environment for the same issue-to-draft-change loop.
- Build a broad provider abstraction now: rejected because checks, reviews,
  comments, webhooks, and plugin/provider marketplaces would expand v0 before
  the delivery loop is proven.
- Add a narrow GitLab slice through existing boundaries: accepted because it
  exercises the provider-instance model without changing source-of-truth or
  approval boundaries.

## Consequences

GitLab work must remain scoped to issue snapshots and draft MR delivery until a
later roadmap or ADR broadens provider behavior. Self-hosted instances require
explicit GitLab provider configuration when repository URL inference cannot
distinguish a generic Git host from GitLab.
