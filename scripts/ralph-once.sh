#!/usr/bin/env bash
# Run one human-in-the-loop Ralph iteration in a fresh agent session.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null)"; then
  :
else
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
fi
source "$SCRIPT_DIR/lib/ralph-worktree.sh"
source "$SCRIPT_DIR/lib/ralph-agent.sh"
cd "$ROOT"

usage() {
  cat <<'EOF'
Usage:
  scripts/ralph-once.sh [--issue <number>] [--agent <auto|codebuddy|codex|claude>] [--worktree <name>] [--branch <branch>] [--base <branch>] [--no-worktree] [-- <agent command...>]

Examples:
  scripts/ralph-once.sh
  scripts/ralph-once.sh --issue 123
  scripts/ralph-once.sh --agent codex --issue 123
  scripts/ralph-once.sh --agent claude --issue 123
  scripts/ralph-once.sh --issue 123 --worktree issue-123-runner --branch ralph/issue-123-runner
  scripts/ralph-once.sh --no-worktree
  scripts/ralph-once.sh --agent auto --issue 123
  scripts/ralph-once.sh --issue 123 -- <custom agent command...>

Runs exactly one HITL Ralph iteration. The script starts a fresh agent session
in an isolated worktree by default and passes docs/agents/ralph.md as the
source of truth. It does not run an AFK loop.
EOF
}

issue=""
worktree_name=""
branch=""
base=""
use_worktree=true
agent=()
agent_choice="auto"
agent_choice_explicit=false

while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -i|--issue)
      if [ "$#" -lt 2 ]; then
        echo "Error: --issue requires an issue number" >&2
        exit 1
      fi
      issue="$2"
      shift 2
      ;;
    --worktree)
      if [ "$#" -lt 2 ]; then
        echo "Error: --worktree requires a name" >&2
        exit 1
      fi
      worktree_name="$2"
      shift 2
      ;;
    --branch)
      if [ "$#" -lt 2 ]; then
        echo "Error: --branch requires a branch name" >&2
        exit 1
      fi
      branch="$2"
      shift 2
      ;;
    --base)
      if [ "$#" -lt 2 ]; then
        echo "Error: --base requires a branch name" >&2
        exit 1
      fi
      base="$2"
      shift 2
      ;;
    --no-worktree)
      use_worktree=false
      shift
      ;;
    --agent)
      if [ "$#" -lt 2 ]; then
        echo "Error: --agent requires one of: $(ralph_agent_values)" >&2
        exit 1
      fi
      agent_choice="$2"
      agent_choice_explicit=true
      ralph_validate_agent_choice "$agent_choice"
      shift 2
      ;;
    --)
      shift
      agent=("$@")
      break
      ;;
    *)
      echo "Error: unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [ ! -f docs/agents/ralph.md ]; then
  echo "Error: docs/agents/ralph.md not found" >&2
  exit 1
fi

run_dir="$ROOT"

if [ "$use_worktree" = true ]; then
  if [ -z "$base" ]; then
    base="$(ralph_default_base)"
  fi

  if [ -z "$worktree_name" ]; then
    worktree_name="$(ralph_default_worktree_name once "$issue")"
  fi

  ralph_validate_worktree_name "$worktree_name"

  if [ -z "$branch" ]; then
    branch="$(ralph_default_branch "$worktree_name")"
  fi

  run_dir="$(ralph_ensure_worktree "$ROOT" "$worktree_name" "$branch" "$base")"
fi

if [ "${#agent[@]}" -gt 0 ] && [ "$agent_choice_explicit" = true ]; then
  echo "Error: --agent cannot be combined with a custom command after --" >&2
  exit 1
fi

if [ "${#agent[@]}" -eq 0 ]; then
  ralph_select_agent hitl "$run_dir" "$agent_choice" "$SCRIPT_DIR"
  agent=("${RALPH_AGENT[@]}")
fi

issue_clause=""
if [ -n "$issue" ]; then
  issue_clause=" for issue #$issue"
fi

prompt="Read docs/agents/ralph.md and run one HITL Ralph iteration${issue_clause}.

Do not run an AFK loop. Before editing files, stop and report:
- selected issue
- why it is the next issue
- parent PRD, if any
- acceptance criteria
- intended verification commands
- whether the TDD workflow applies
- stop condition"

cd "$run_dir"
exec "${agent[@]}" "$prompt"
