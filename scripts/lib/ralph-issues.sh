#!/usr/bin/env bash
# Shared Ralph issue-scope helpers.

ralph_parse_issue_scope() {
  local spec="$1"
  local token
  local start
  local end
  local issue
  local tokens=()
  local issues=()

  if [ -z "$spec" ]; then
    echo "Error: --issues requires a comma-separated issue list or range" >&2
    exit 1
  fi

  IFS=',' read -r -a tokens <<<"$spec"

  for token in "${tokens[@]}"; do
    token="${token//[[:space:]]/}"

    case "$token" in
      "")
        echo "Error: --issues contains an empty item" >&2
        exit 1
        ;;
      [0-9]*-[0-9]*)
        start="${token%-*}"
        end="${token#*-}"

        if ! [[ "$start" =~ ^[1-9][0-9]*$ && "$end" =~ ^[1-9][0-9]*$ ]]; then
          echo "Error: invalid --issues range '$token'" >&2
          exit 1
        fi

        if [ "$start" -gt "$end" ]; then
          echo "Error: invalid --issues range '$token' (start must be <= end)" >&2
          exit 1
        fi

        for ((issue = start; issue <= end; issue++)); do
          issues+=("$issue")
        done
        ;;
      [0-9]*)
        if ! [[ "$token" =~ ^[1-9][0-9]*$ ]]; then
          echo "Error: invalid --issues item '$token'" >&2
          exit 1
        fi
        issues+=("$token")
        ;;
      *)
        echo "Error: invalid --issues item '$token'" >&2
        exit 1
        ;;
    esac
  done

  if [ "${#issues[@]}" -eq 0 ]; then
    echo "Error: --issues did not contain any issue numbers" >&2
    exit 1
  fi

  RALPH_ISSUES=("${issues[@]}")
}

ralph_issue_scope_sentence() {
  local prefix="$1"
  shift
  local issue
  local sentence="$prefix"
  local separator=""

  for issue in "$@"; do
    sentence+="${separator}#${issue}"
    separator=", "
  done

  printf '%s\n' "$sentence"
}
