# Domain Docs

This file tells agents how to consume project domain documentation before
planning, debugging, implementation, or refactoring.

## Before Exploring

Read these first when they are relevant to the task:

- `CONTEXT.md` at the repository root for product vocabulary, source-of-truth
  boundaries, and invariants.
- `ROADMAP.md` for version boundaries and non-goals.
- `docs/adr/` for architectural decision records.

If one of these files does not exist, proceed silently. Do not create missing
domain docs unless the task resolves a real term, invariant, or architectural
decision.

## Layout

Current layout is single-context:

```text
/
├── CONTEXT.md
├── ROADMAP.md
├── docs/
│   ├── agents/
│   └── adr/
└── .scratch/
    └── <slug>/          # local PRD/issues fallback only
```

Create a context map only after multiple product areas, packages, or services
gain durable vocabulary or invariants that no longer fit in the root
`CONTEXT.md`.

## Vocabulary Rule

Use the names in `CONTEXT.md` when writing:

- issue titles and PRDs,
- test names,
- API contract notes,
- refactor plans,
- code comments,
- debugging hypotheses.

Do not drift to synonyms that the relevant context explicitly avoids. If a
necessary concept is missing, flag it and propose a vocabulary update.

## ADR Rule

Create or propose an ADR only when all are true:

1. the decision is hard to reverse,
2. the choice would be surprising without context,
3. the choice comes from a real trade-off.

If a plan contradicts an existing ADR, surface the conflict explicitly before
implementation.
