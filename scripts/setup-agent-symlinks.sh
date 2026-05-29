#!/usr/bin/env bash
# setup-agent-symlinks.sh
# Create symlinks so agent tools can find shared repo guidance and skills.
# Idempotent — safe to re-run.

set -euo pipefail
cd "$(git -C "$(dirname "$0")" rev-parse --show-toplevel 2>/dev/null || dirname "$0")"

SKILLS_TARGET=".agents/skills"
GUIDANCE_TARGET="AGENTS.md"

if [ ! -d "$SKILLS_TARGET" ]; then
  echo "Error: $SKILLS_TARGET not found" >&2; exit 1
fi

if [ ! -f "$GUIDANCE_TARGET" ]; then
  echo "Error: $GUIDANCE_TARGET not found" >&2; exit 1
fi

create_link() {
  local link="$1"
  local target="$2"
  local link_dir

  link_dir="$(dirname "$link")"
  mkdir -p "$link_dir"

  if [ -L "$link" ]; then
    if [ "$(readlink "$link")" = "$target" ]; then
      echo "Symlink already correct: $link -> $target"
      return 0
    fi
    rm "$link"
  elif [ -e "$link" ]; then
    echo "Error: $link exists and is not a symlink. Remove it and re-run." >&2
    exit 1
  fi

  ln -s "$target" "$link"
  echo "Created: $link -> $target"
}

create_link ".codebuddy/skills" "../$SKILLS_TARGET"
create_link ".claude/skills" "../$SKILLS_TARGET"
create_link "CLAUDE.md" "$GUIDANCE_TARGET"
