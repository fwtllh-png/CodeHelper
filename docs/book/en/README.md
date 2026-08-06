# CodeHelper Agent Engineering Book

[简体中文](../zh-CN/README.md) | English

This book teaches Agent engineering through a real, governed runtime. It moves
from foundations and observable behavior into protocols, source code,
security, durable execution, orchestration, hosts, extensions, and practical
labs.

The book is under construction. A visible catalog entry does not mean a
chapter has been delivered.

## Start Here

- [Complete navigation and chapter status](./NAVIGATION.md)
- [Glossary](./glossary.md)
- [Knowledge documentation plan](../../en/knowledge-base-plan.md)
- [English chapter template](./_templates/chapter.md)
- Machine-readable catalog: [`docs/book/catalog.json`](../catalog.json)
- Front Matter contract:
  [`docs/book/schema/chapter.schema.json`](../schema/chapter.schema.json)

## Status Model

| Status | Meaning |
| --- | --- |
| `planned` | The chapter exists only in the catalog and navigation. |
| `draft` | Both language files exist, but the chapter is incomplete or unverified. |
| `verified` | Content, source references, tests, commands, and bilingual parity passed the chapter gate. |

Only `draft` and `verified` chapters have Markdown files. This keeps missing
work visible without filling the repository with empty placeholders.

## Reading Strategy

New readers should follow the Stage 1 path in
[Navigation](./NAVIGATION.md). Experienced contributors may enter through a
module-specific part, but should read the system architecture and runtime
vocabulary first.

Each chapter connects:

```text
technical background
  -> CodeHelper design
  -> package and contract map
  -> implementation walkthrough
  -> failure and security analysis
  -> tests and reproducible lab
```

## Authoring Workflow

1. Select a `planned` chapter from `docs/book/catalog.json`.
2. Change its status to `draft`.
3. Copy both language templates to the catalog-derived path.
4. Replace all placeholders and set matching Front Matter IDs and status.
5. Update prerequisites and real source/test paths.
6. Render navigation:

   ```bash
   python3 scripts/render-book-navigation.py
   ```

7. Run:

   ```bash
   make book-check
   make docs-check
   ```

8. Change the chapter to `verified` only after every Definition of Done item in
   the [documentation plan](../../en/knowledge-base-plan.md) is satisfied.

## Facts and Plans

- Product manuals describe current behavior.
- Book chapters explain background, design, implementation, and evidence.
- The catalog declares delivery status.
- Code, tests, schemas, and generated contracts remain authoritative.
- Roadmap material must be marked explicitly and never presented as shipped.
