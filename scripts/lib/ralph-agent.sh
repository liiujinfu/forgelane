#!/usr/bin/env bash
# Shared Ralph agent-selection helpers.

ralph_agent_values() {
  printf 'auto|codebuddy|codex|claude\n'
}

ralph_validate_agent_choice() {
  local choice="$1"

  case "$choice" in
    auto|codebuddy|codex|claude)
      ;;
    *)
      echo "Error: --agent must be one of: $(ralph_agent_values)" >&2
      exit 1
      ;;
  esac
}

ralph_resolve_auto_agent() {
  if command -v codebuddy >/dev/null 2>&1; then
    printf 'codebuddy\n'
  elif command -v codex >/dev/null 2>&1; then
    printf 'codex\n'
  elif command -v claude >/dev/null 2>&1; then
    printf 'claude\n'
  else
    echo "Error: no default agent found. Use --agent or pass a command after --." >&2
    exit 1
  fi
}

ralph_require_agent_command() {
  local choice="$1"
  local command_name="$2"

  if ! command -v "$command_name" >/dev/null 2>&1; then
    echo "Error: --agent $choice requires the $command_name command" >&2
    exit 1
  fi
}

ralph_select_agent() {
  local mode="$1"
  local run_dir="$2"
  local choice="$3"
  local script_dir="$4"

  ralph_validate_agent_choice "$choice"

  if [ "$choice" = auto ]; then
    choice="$(ralph_resolve_auto_agent)"
  fi

  RALPH_AGENT=()

  case "$choice" in
    codebuddy)
      ralph_require_agent_command "$choice" codebuddy
      if [ "$mode" = hitl ]; then
        RALPH_AGENT=(codebuddy)
      else
        RALPH_AGENT=(codebuddy -p --permission-mode acceptEdits)
      fi
      ;;
    codex)
      ralph_require_agent_command "$choice" codex
      if [ "$mode" = hitl ]; then
        RALPH_AGENT=(codex --cd "$run_dir")
      else
        RALPH_AGENT=(codex exec --cd "$run_dir" --sandbox workspace-write --ask-for-approval never)
      fi
      ;;
    claude)
      ralph_require_agent_command "$choice" claude
      if [ "$mode" = hitl ]; then
        RALPH_AGENT=("$script_dir/claude-with-cc-switch.mjs" --permission-mode acceptEdits)
      else
        RALPH_AGENT=("$script_dir/claude-with-cc-switch.mjs" -p --permission-mode acceptEdits)
      fi
      ;;
    *)
      echo "Error: unsupported Ralph agent '$choice'" >&2
      exit 1
      ;;
  esac
}
