# v0 GitHub Provider Uses the GitHub API

ForgeLane v0 will implement the in-product GitHub provider with GitHub
REST/GraphQL API calls rather than shelling out to the `gh` CLI. The `gh` CLI
can remain useful for repository maintenance and agent workflow scripts, but the
ForgeLane provider boundary should not depend on local CLI state, output formats,
or installed developer tooling.

## Considered Options

- Shell out to `gh`: rejected because it would bind product behavior to local
  CLI configuration and make provider behavior harder to test in process.
- Use provider APIs directly: accepted because it keeps authentication,
  request/response handling, idempotency, and test doubles inside the provider
  boundary.

## Consequences

The first GitHub provider slice can stay narrow: read an issue snapshot for a
WorkItem. Branch, draft PR, checks, reviews, and comments can be added in later
slices without changing the provider boundary.
