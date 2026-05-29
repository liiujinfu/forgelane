#!/usr/bin/env bash
# Shared Ralph branch and worktree helpers.

ralph_timestamp() {
  date +%Y%m%d-%H%M%S
}

ralph_default_worktree_name() {
  local mode="$1"
  local issue="${2:-}"

  if [ -n "$issue" ]; then
    printf 'issue-%s\n' "$issue"
  else
    printf '%s-%s\n' "$mode" "$(ralph_timestamp)"
  fi
}

ralph_validate_worktree_name() {
  local worktree_name="$1"

  case "$worktree_name" in
    *[!A-Za-z0-9._-]*|"")
      echo "Error: --worktree may only contain letters, numbers, dot, underscore, and dash" >&2
      exit 1
      ;;
  esac
}

ralph_default_branch() {
  local worktree_name="$1"
  printf 'ralph/%s\n' "$worktree_name"
}

ralph_default_base() {
  local base

  if base="$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null)"; then
    printf '%s\n' "${base#origin/}"
  elif git show-ref --verify --quiet refs/remotes/origin/master; then
    printf 'master\n'
  else
    git branch --show-current
  fi
}

ralph_fetch_base() {
  local base="$1"

  if ! git remote get-url origin >/dev/null 2>&1; then
    echo "Ralph: origin remote not configured; using local base '$base'" >&2
    return 0
  fi

  if git show-ref --verify --quiet "refs/remotes/origin/$base"; then
    git fetch origin "$base"
  else
    git fetch origin
  fi
}

ralph_resolve_base_ref() {
  local base="$1"

  if git show-ref --verify --quiet "refs/remotes/origin/$base"; then
    printf 'origin/%s\n' "$base"
  else
    printf '%s\n' "$base"
  fi
}

ralph_ensure_worktree() {
  local root="$1"
  local worktree_name="$2"
  local branch="$3"
  local base="$4"
  local worktree_dir="$root/.scratch/worktrees/$worktree_name"
  local base_ref

  mkdir -p "$root/.scratch/worktrees"

  if [ ! -d "$worktree_dir/.git" ] && [ ! -f "$worktree_dir/.git" ]; then
    ralph_fetch_base "$base"
    base_ref="$(ralph_resolve_base_ref "$base")"

    if git show-ref --verify --quiet "refs/heads/$branch"; then
      git worktree add "$worktree_dir" "$branch" >&2
    else
      git worktree add -b "$branch" "$worktree_dir" "$base_ref" >&2
    fi
  fi

  printf '%s\n' "$worktree_dir"
}
