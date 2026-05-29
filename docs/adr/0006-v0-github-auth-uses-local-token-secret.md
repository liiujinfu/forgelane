# v0 GitHub Auth Uses a Local Token Secret

ForgeLane v0 will authenticate to GitHub with a locally configured token or PAT
treated as secret material. OAuth, GitHub App installation flows, and
provider-backed user identity binding are deferred until ForgeLane moves beyond
the trusted single-user/self-hosted baseline.

## Considered Options

- OAuth from day one: rejected because callback handling and user identity
  binding would expand v0 beyond the first delivery loop.
- GitHub App from day one: rejected because installation, permission, and
  organization mapping are useful later but not required to prove the provider
  source-of-truth and audit boundaries.
- Local token secret: accepted because it gives the v0 GitHub API provider a
  real authentication path while keeping the deployment model small.

## Consequences

GitHub tokens must not be stored in normal repository config, events, logs, or
artifacts. The first implementation can read them from environment variables or
local self-hosted secret configuration and later replace that source without
changing the provider boundary.
