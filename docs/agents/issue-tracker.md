# Issue Tracker

Issues and PRDs for this repo live in GitHub:

```text
https://github.com/liiujinfu/forgelane
```

Use GitHub Issues as the default task tracker. If GitHub CLI access is
unavailable or the task is explicitly local-only, use the local markdown
fallback described below.

## GitHub Conventions

- Do not create smoke or test issues to verify GitHub access unless the user
  explicitly asks for a tracker smoke test. Prefer read-only checks such as
  `gh issue list` and `gh issue view <number> --comments`.
- Create an issue: `gh issue create --title "..." --body "..."`
- Read an issue: `gh issue view <number> --comments`
- List issues: `gh issue list --state open --json number,title,labels,state,updatedAt`
- Comment: `gh issue comment <number> --body "..."`
- Apply labels: `gh issue edit <number> --add-label "..."`
- Remove labels: `gh issue edit <number> --remove-label "..."`
- Close: post a closing note first with `gh issue comment`, then run
  `gh issue close <number>`

Infer the repository from `git remote -v` when running inside this clone. Use
`--repo liiujinfu/forgelane` only when running from outside the repository.

## Local Markdown Fallback

When GitHub access is unavailable or the user asks for local-only planning,
store planning artifacts under `.scratch/`:

```text
.scratch/<slug>/
├── PRD.md
└── issues/
    ├── 01-<vertical-slice>.md
    └── 02-<vertical-slice>.md
```

Each issue file should include:

- `Status:` using the vocabulary in `docs/agents/triage-labels.md`
- problem/context
- acceptance criteria
- verification commands
- dependencies/blockers
- comments appended under `## Comments`

## PRD And Issue Shape

- PRDs describe the destination: user-visible behavior, constraints, non-goals,
  implementation decisions, and testing decisions.
- Issues describe the journey: narrow vertical slices that can be independently
  implemented and verified.
- Prefer vertical slices over horizontal tasks. A good issue cuts through the
  necessary provider/API/runner/UI/test layers for one user-observable behavior.
- Keep template headings and structural fields in English, such as `Parent`,
  `What to build`, `Acceptance criteria`, and `Blocked by`.
- Write issue and PRD narrative content in English by default, regardless of the
  language used in the planning conversation. Use another language only when the
  maintainer explicitly requests it. Always preserve code identifiers, API
  paths, commands, labels, statuses, and other exact technical terms.
