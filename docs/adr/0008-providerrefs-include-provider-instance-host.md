# Canonical ProviderRefs Include the Provider Instance Host

ForgeLane will make canonical ProviderRefs URL-like and include the provider
instance host, such as `github://github.com/owner/repo/issues/123`, instead of
using hostless short refs such as `github:owner/repo#123`. This keeps WorkItem,
PR/MR, commit, review, and CI references globally unambiguous across public
GitHub/GitLab, GitHub Enterprise, and private GitLab instances while still
allowing CLI shorthand to resolve through repository config.

## Considered Options

- Hostless refs: rejected because `gitlab:group/project#123` is ambiguous across
  GitLab instances and would make provider-owned source identity depend on
  surrounding configuration.
- URL-like refs with provider host: accepted because the provider type, provider
  instance, repository path, object kind, and object id remain explicit in the
  persisted and audited identity.
