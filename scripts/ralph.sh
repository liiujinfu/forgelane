#!/usr/bin/env bash
# Run a bounded AFK Ralph loop. Each iteration starts a fresh agent session.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if ROOT="$(git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null)"; then
  :
else
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
fi
source "$SCRIPT_DIR/lib/ralph-worktree.sh"
source "$SCRIPT_DIR/lib/ralph-agent.sh"
source "$SCRIPT_DIR/lib/ralph-issues.sh"
cd "$ROOT"

usage() {
  cat <<'EOF'
Usage:
  scripts/ralph.sh <iterations> [--agent <auto|codebuddy|codex|claude>] [--issues <list|range>] [--parallel|--serial] [--worktree <name>] [--branch <branch>] [--base <branch>] [--pr] [-- <agent command...>]

Examples:
  scripts/ralph.sh 5
  scripts/ralph.sh 3 --agent codex
  scripts/ralph.sh 3 --agent codex --issues 19,20,21
  scripts/ralph.sh 3 --agent codex --issues 19,20,21 --serial
  scripts/ralph.sh 5 --issues 19-25
  scripts/ralph.sh 3 --agent claude
  scripts/ralph.sh 5 --pr
  scripts/ralph.sh 10 --worktree forgelane-runner --branch ralph/forgelane-runner --pr
  scripts/ralph.sh 3 --agent auto
  scripts/ralph.sh 3 -- <custom agent command...>

Runs a bounded AFK Ralph loop. Each iteration starts a fresh agent session,
implements at most one eligible issue, verifies it, commits only on passing
verification, and stops early when the agent outputs COMPLETE.

By default this creates an isolated worktree under .scratch/worktrees/ and a
branch named ralph/afk-<timestamp>. Use --pr to push the branch and create a
GitHub draft pull request after a successful, unblocked run.

With multiple --issues, Ralph runs one bounded loop per issue concurrently by
default. Parallel runs create one worktree and branch per issue, named
issue-<number> and ralph/issue-<number>. Use --serial to keep a scoped issue
set on one afk branch.
EOF
}

if [ "$#" -eq 0 ]; then
  usage >&2
  exit 1
fi

case "$1" in
  -h|--help)
    usage
    exit 0
    ;;
esac

iterations="$1"
shift

if ! [[ "$iterations" =~ ^[1-9][0-9]*$ ]]; then
  echo "Error: iterations must be a positive integer" >&2
  usage >&2
  exit 1
fi

worktree_name=""
branch=""
base=""
create_pr=false
parallel=false
serial=false
agent=()
agent_choice="auto"
agent_choice_explicit=false
issues_spec=""

while [ "$#" -gt 0 ]; do
  case "$1" in
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
    --pr|--mr)
      create_pr=true
      shift
      ;;
    --parallel)
      parallel=true
      shift
      ;;
    --serial)
      serial=true
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
    --issues)
      if [ "$#" -lt 2 ]; then
        echo "Error: --issues requires a comma-separated issue list or range" >&2
        exit 1
      fi
      issues_spec="$2"
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

if [ "${#agent[@]}" -gt 0 ] && [ "$agent_choice_explicit" = true ]; then
  echo "Error: --agent cannot be combined with a custom command after --" >&2
  exit 1
fi

if [ "$parallel" = true ] && [ "$serial" = true ]; then
  echo "Error: --parallel cannot be combined with --serial" >&2
  exit 1
fi

if [ ! -f docs/agents/ralph.md ]; then
  echo "Error: docs/agents/ralph.md not found" >&2
  exit 1
fi

issue_scope_clause=""
issue_scope_prompt=""
if [ -n "$issues_spec" ]; then
  ralph_parse_issue_scope "$issues_spec"
  issue_scope_clause="$(ralph_issue_scope_sentence "Only select eligible ready-for-agent child issues from this issue scope: " "${RALPH_ISSUES[@]}")."
  issue_scope_prompt="$issue_scope_clause
"
fi

if [ -z "$base" ]; then
  base="$(ralph_default_base)"
fi

parallel_prompt_for_issue() {
  local issue="$1"

  cat <<EOF
Read docs/agents/ralph.md and run one AFK Ralph iteration for issue #$issue.

Use .scratch/ralph-progress.md as the progress file if it exists.
Implement only issue #$issue.
Do not select or modify any other issue.
Run the smallest verification set that proves issue #$issue.
If the issue changes observable behavior and a practical public test boundary exists, use the TDD workflow before implementation.
Commit only if verification passes.
Append concise progress to .scratch/ralph-progress.md.

If issue #$issue is already complete after verification, output COMPLETE.
If issue #$issue is not eligible or is blocked by a missing human decision, output BLOCKED: followed by the blocker.
Do not ask for human input during this AFK iteration.
EOF
}

run_parallel_issue() {
  local issue="$1"
  local issue_worktree_name="issue-$issue"
  local issue_branch
  local issue_worktree_dir
  local issue_prompt
  local output
  local status
  local selected_agent=()
  local i

  issue_branch="$(ralph_default_branch "$issue_worktree_name")"

  echo "== Ralph parallel issue #$issue =="
  echo "Worktree: $issue_worktree_name"
  echo "Branch: $issue_branch"

  issue_worktree_dir="$(ralph_ensure_worktree "$ROOT" "$issue_worktree_name" "$issue_branch" "$base")"
  cd "$issue_worktree_dir"
  mkdir -p .scratch

  if [ "${#agent[@]}" -eq 0 ]; then
    ralph_select_agent afk "$issue_worktree_dir" "$agent_choice" "$SCRIPT_DIR"
    selected_agent=("${RALPH_AGENT[@]}")
  else
    selected_agent=("${agent[@]}")
  fi

  issue_prompt="$(parallel_prompt_for_issue "$issue")"

  for ((i = 1; i <= iterations; i++)); do
    echo "== Ralph issue #$issue iteration $i/$iterations =="

    set +e
    output="$("${selected_agent[@]}" "$issue_prompt" 2>&1)"
    status="$?"
    set -e

    printf '%s\n' "$output"

    if [ "$status" -ne 0 ]; then
      echo "Ralph issue #$issue iteration failed with exit code $status" >&2
      return "$status"
    fi

    if [[ "$output" == *"COMPLETE"* ]]; then
      echo "Ralph issue #$issue complete after $i iteration(s)."
      return 0
    fi

    if [[ "$output" == *"BLOCKED:"* ]]; then
      echo "Ralph issue #$issue blocked after $i iteration(s)." >&2
      return 2
    fi
  done

  echo "Ralph issue #$issue stopped after $iterations bounded iteration(s)."
}

create_parallel_pull_requests() {
  local issue
  local issue_worktree_name
  local issue_branch
  local issue_worktree_dir
  local base_ref
  local commit_count
  local body

  if [ "$create_pr" != true ]; then
    return 0
  fi

  if ! command -v gh >/dev/null 2>&1; then
    echo "Error: --pr requires gh" >&2
    exit 1
  fi

  base_ref="$(ralph_resolve_base_ref "$base")"

  for issue in "${RALPH_ISSUES[@]}"; do
    issue_worktree_name="issue-$issue"
    issue_branch="$(ralph_default_branch "$issue_worktree_name")"
    issue_worktree_dir="$ROOT/.scratch/worktrees/$issue_worktree_name"

    commit_count="$(git -C "$issue_worktree_dir" rev-list --count "$base_ref"..HEAD)"
    if [ "$commit_count" = "0" ]; then
      echo "No commits were created for issue #$issue; skipping PR."
      continue
    fi

    git -C "$issue_worktree_dir" push -u origin "$issue_branch"

    body="Parallel AFK Ralph run.

Source: docs/agents/ralph.md
Worktree: $issue_worktree_name
Branch: $issue_branch
Base: $base
Issue: #$issue
Iterations requested per issue: $iterations

Each committed iteration should include issue references and verification evidence."

    gh pr create \
      --head "$issue_branch" \
      --base "$base" \
      --title "Ralph: issue-$issue" \
      --body "$body" \
      --draft
  done
}

run_parallel_ralph() {
  local run_name
  local run_dir
  local issue
  local issue_worktree_name
  local issue_branch
  local log_file
  local pids=()
  local issue_numbers=()
  local log_files=()
  local seen_issues=""
  local i
  local status
  local failed=false
  local blocked=false

  if [ -z "$issues_spec" ]; then
    echo "Error: --parallel requires --issues" >&2
    exit 1
  fi

  if [ -n "$worktree_name" ] || [ -n "$branch" ]; then
    echo "Error: parallel issue runs derive one worktree and branch per issue; use --serial to combine --issues with --worktree or --branch" >&2
    exit 1
  fi

  for issue in "${RALPH_ISSUES[@]}"; do
    case ",$seen_issues," in
      *",$issue,"*)
        echo "Error: --parallel received duplicate issue #$issue" >&2
        exit 1
        ;;
    esac
    seen_issues="${seen_issues:+$seen_issues,}$issue"
  done

  run_name="ralph-parallel-$(ralph_timestamp)"
  run_dir="$ROOT/.scratch/$run_name"
  mkdir -p "$run_dir"

  echo "== Ralph parallel run =="
  echo "Issues: ${RALPH_ISSUES[*]}"
  echo "Iterations per issue: $iterations"
  echo "Logs: $run_dir"

  for issue in "${RALPH_ISSUES[@]}"; do
    issue_worktree_name="issue-$issue"
    issue_branch="$(ralph_default_branch "$issue_worktree_name")"
    echo "Preparing issue #$issue worktree: $issue_worktree_name ($issue_branch)"
    ralph_ensure_worktree "$ROOT" "$issue_worktree_name" "$issue_branch" "$base" >/dev/null
  done

  for issue in "${RALPH_ISSUES[@]}"; do
    log_file="$run_dir/issue-$issue.log"
    run_parallel_issue "$issue" >"$log_file" 2>&1 &
    pids+=("$!")
    issue_numbers+=("$issue")
    log_files+=("$log_file")
  done

  for i in "${!pids[@]}"; do
    if wait "${pids[$i]}"; then
      status=0
    else
      status="$?"
    fi

    case "$status" in
      0)
        echo
        echo "== Ralph issue #${issue_numbers[$i]} log =="
        cat "${log_files[$i]}"
        echo "== Ralph issue #${issue_numbers[$i]} succeeded =="
        ;;
      2)
        echo
        echo "== Ralph issue #${issue_numbers[$i]} log =="
        cat "${log_files[$i]}"
        echo "== Ralph issue #${issue_numbers[$i]} blocked ==" >&2
        blocked=true
        ;;
      *)
        echo
        echo "== Ralph issue #${issue_numbers[$i]} log =="
        cat "${log_files[$i]}"
        echo "== Ralph issue #${issue_numbers[$i]} failed with exit code $status ==" >&2
        failed=true
        ;;
    esac
  done

  if [ "$failed" = true ]; then
    exit 1
  fi

  if [ "$blocked" = true ]; then
    exit 2
  fi

  create_parallel_pull_requests
  echo "Ralph parallel run complete."
}

if [ "$parallel" = true ] || { [ -n "$issues_spec" ] && [ "$serial" != true ] && [ "${#RALPH_ISSUES[@]}" -gt 1 ]; }; then
  run_parallel_ralph
  exit 0
fi

if [ -z "$worktree_name" ]; then
  if [ -n "$issues_spec" ] && [ "$serial" != true ] && [ "${#RALPH_ISSUES[@]}" -eq 1 ]; then
    worktree_name="$(ralph_default_worktree_name afk "${RALPH_ISSUES[0]}")"
  else
    worktree_name="$(ralph_default_worktree_name afk)"
  fi
fi

ralph_validate_worktree_name "$worktree_name"

if [ -z "$branch" ]; then
  branch="$(ralph_default_branch "$worktree_name")"
fi

worktree_dir="$(ralph_ensure_worktree "$ROOT" "$worktree_name" "$branch" "$base")"

cd "$worktree_dir"
start_commit="$(git rev-parse HEAD)"

if [ "${#agent[@]}" -eq 0 ]; then
  ralph_select_agent afk "$worktree_dir" "$agent_choice" "$SCRIPT_DIR"
  agent=("${RALPH_AGENT[@]}")
fi

mkdir -p .scratch

create_pull_request() {
  if [ "$create_pr" != true ]; then
    return 0
  fi

  if ! command -v gh >/dev/null 2>&1; then
    echo "Error: --pr requires gh" >&2
    exit 1
  fi

  if [ "$(git rev-list --count "$start_commit"..HEAD)" = "0" ]; then
    echo "No commits were created after Ralph started; skipping PR."
    return 0
  fi

  git push -u origin "$branch"

  body="AFK Ralph run.

Source: docs/agents/ralph.md
Worktree: $worktree_name
Branch: $branch
Base: $base
Iterations requested: $iterations

Each committed iteration should include issue references and verification evidence."

  gh pr create \
    --head "$branch" \
    --base "$base" \
    --title "Ralph: $worktree_name" \
    --body "$body" \
    --draft
}

prompt="Read docs/agents/ralph.md and run one AFK Ralph iteration.

Use .scratch/ralph-progress.md as the progress file if it exists.
Pick one eligible ready-for-agent child issue.
${issue_scope_prompt}Implement only that issue.
Run the smallest verification set that proves the issue.
If the issue changes observable behavior and a practical public test boundary exists, use the TDD workflow before implementation.
Commit only if verification passes.
Append concise progress to .scratch/ralph-progress.md.

If no eligible issues remain, output COMPLETE.
If blocked by a missing human decision, output BLOCKED: followed by the blocker.
Do not ask for human input during this AFK iteration."

for ((i = 1; i <= iterations; i++)); do
  echo "== Ralph iteration $i/$iterations =="

  set +e
  output="$("${agent[@]}" "$prompt" 2>&1)"
  status="$?"
  set -e

  printf '%s\n' "$output"

  if [ "$status" -ne 0 ]; then
    echo "Ralph iteration failed with exit code $status" >&2
    exit "$status"
  fi

  if [[ "$output" == *"COMPLETE"* ]]; then
    echo "Ralph complete after $i iteration(s)."
    create_pull_request
    exit 0
  fi

  if [[ "$output" == *"BLOCKED:"* ]]; then
    echo "Ralph blocked after $i iteration(s)." >&2
    exit 2
  fi
done

echo "Ralph stopped after $iterations bounded iteration(s)."
create_pull_request
