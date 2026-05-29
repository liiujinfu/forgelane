# Triage Labels

The agent skills use five canonical triage roles. Map them to the actual GitHub
labels or local markdown `Status:` values used in this repository.

| Canonical role | Tracker label / local status | Meaning |
| --- | --- | --- |
| `needs-triage` | `needs-triage` | Maintainer needs to evaluate scope, priority, and ownership |
| `needs-info` | `needs-info` | Waiting on reporter or product owner for missing information |
| `ready-for-agent` | `ready-for-agent` | Fully specified and safe for an AFK agent to pick up |
| `ready-for-human` | `ready-for-human` | Requires human implementation, decision, credential, or environment access |
| `wontfix` | `wontfix` | Will not be actioned |

When a skill says to apply a triage role, use the corresponding value from the
right-hand column.

If the GitHub repository already uses different label names, update this table
instead of creating duplicate labels.
