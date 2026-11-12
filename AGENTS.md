# Working on Camp

Use [CONTRIBUTING](CONTRIBUTING.md) for setup/checks and the [documentation map](docs/README.md) to find the owning contract. Read the sections affected by the task, then their source and callers. Reuse unchanged instructions already read; expand context when another ownership boundary is involved. Do not load every guide, historical report or runtime README for each task.

- Inspect the owning repository's Git status before editing; preserve existing work. Shared dependency checkouts are read-only: use public APIs and pin shipped dependencies inside Camp.
- Follow the pinned toolchains, package boundaries and formatters. Keep code, comments and documentation in English; preserve user data and external output as authored.
- Keep secrets in ignored Server configuration (`.env`, mode 600), outside frontend variables, tool environments, logs and tracked files. Keep generated runtime/build/test data ignored.
- Preserve user data, Runner identities/journals and immutable originals. Use append-only team-data migrations; regenerate sqlc with SQL changes. Never clear a database to repair an upgrade.
- Preserve explicit execution authorization, frozen inputs, current permissions and exact file versions. Private drafts, collaboration drafts, shared publication, human approval and delivery have distinct owners and transitions.
- Give each lifecycle one owner. Preserve unsaved work and request IDs after failed/uncertain writes; verify asynchronous ordering, cancellation and recovery.
- Update callers, relevant tests and the owning contract together. Apply the [UI review checklist](docs/design.md#review-checklist) for interaction changes, including direct before/after PR images. Run applicable checks and report actual outcomes/skips.
- Follow the [product interaction principles](docs/design.md#product-principles): favor authorized background progress, reuse existing destinations and interrupt people only for their current responsibility. Avoid unnecessary secondary labels and descriptions. Let the layout do the explaining. Apply [content hierarchy](docs/design.md#content-and-information-hierarchy) without hiding required inputs, errors, recovery or decision-changing consequences.
- Follow the [performance contract](docs/performance.md) when adding UI reads, live events, histories or streamed writes. Reuse scoped reads and bounded projections; preserve responsive primary content, durable originals and current authority. Extend the owning invalidation rules and regression checks with the feature.

Follow [documentation maintenance](CONTRIBUTING.md#documentation) for ownership, deduplication and historical evidence. Track unresolved behavior in issues and the owning implementation limits; do not describe planned behavior as shipped.
