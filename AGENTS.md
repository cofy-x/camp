# Working on Camp

Use [CONTRIBUTING](CONTRIBUTING.md) for setup and checks. Use the [documentation map](docs/README.md) to find the owning contract. Read the affected sections, then their source and callers. Reuse that context until the task crosses another ownership boundary.

- Inspect the owning repository's Git status before editing; preserve existing work. Shared dependency checkouts are read-only: use public APIs and pin shipped dependencies inside Camp.
- Follow the pinned toolchains, package boundaries and formatters. Keep code, comments and documentation in English; preserve user data and external output as authored.
- Keep secrets in ignored Server configuration (`.env`, mode 600), outside frontend variables, tool environments, logs and tracked files. Keep generated runtime/build/test data ignored.
- Preserve user data, Runner identities/journals and immutable originals. Use append-only team-data migrations; regenerate sqlc with SQL changes. Never clear a database to repair an upgrade.
- Preserve explicit execution authorization, frozen inputs, current permissions and exact file versions. Private drafts, collaboration drafts, shared publication, human approval and delivery have distinct owners and transitions.
- Give each [lifecycle](docs/lifecycles.md) one owner. Preserve unsaved work and request IDs after failed/uncertain writes; verify asynchronous ordering, cancellation and recovery.
- Update affected callers, tests and the owning contract together. Run applicable checks and report their scope, outcomes and skips.
- For UI changes, follow the [product principles](docs/design.md#product-principles) and reuse the shared [controls](docs/design.md#settings-and-view-controls) and [typography](docs/design.md#typography). Remove repeated copy and actions before simplifying layout and behavior. Keep required inputs, errors, recovery and decision-changing consequences visible.
- Verify affected UI interactions at normal, narrow and short window sizes. Use the [UI review checklist](docs/design.md#review-checklist) for accessibility and before/after evidence.
- Follow the [performance contract](docs/performance.md) when adding UI reads, live events, histories or streamed writes. Reuse scoped reads and bounded projections; preserve responsive primary content, durable originals and current authority. Extend the owning invalidation rules and regression checks with the feature.
- Use the [Work follow-through check](CONTRIBUTING.md#work-follow-through) for changes to GitHub intake, Activity navigation and execution recovery.

Follow [documentation maintenance](CONTRIBUTING.md#documentation) for ownership, deduplication and historical evidence. Track unresolved behavior in issues and the owning implementation limits; do not describe planned behavior as shipped.
