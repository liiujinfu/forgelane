# v0 Uses a Transactional Audit Log

ForgeLane v0 will write current-state rows and audit Events in the same SQLite
transaction for every authoritative state change. Events are immutable audit
records, but v0 will not use pure event sourcing or rebuild current state by
replaying the event log.

## Considered Options

- Pure event sourcing: rejected because projection rebuilds, replay semantics,
  and migration rules would add complexity before the first delivery loop is
  proven.
- State tables without transactional events: rejected because it can create
  unaudited state transitions or events that do not match current state.
- Transactional audit log: accepted because it keeps writes consistent while
  allowing CLI and future UI queries to read current state directly.

## Consequences

All authoritative state changes must go through workflow/store code that updates
state and appends Events atomically. Logs and large provider payloads should not
be stored directly in Events; they should use log chunks, artifacts, provider
refs, or compact snapshots.
