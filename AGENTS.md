# Working on Camp

Use [CONTRIBUTING.md](CONTRIBUTING.md) for setup, checks and pull requests. Before changing an area, read its owning contract:

| Area | Contract |
| --- | --- |
| Code placement, state ownership and recovery | [Architecture](docs/architecture.md) |
| Authentication, permissions and wire contracts | [API](docs/api.md) |
| UI content, controls and interaction review | [Design](docs/design.md) |
| DSH/LayerFS integration, configuration and model limits | [Execution](docs/dsh.md) |
| Native isolation, workspaces and platform acceptance | [Native Runner](docs/native-runner.md) |
| Deployment, releases, upgrades and backup | [Installation](docs/install.md) |

- Inspect Git status before editing and preserve existing changes. Shared dependency checkouts are read-only references; use public APIs and pin runtime dependencies here.
- Follow the pinned toolchains, [package boundaries](docs/architecture.md#package-boundaries) and formatters. Keep code, comments and documentation in English; preserve user data and external output as authored.
- Keep secrets in ignored Server configuration (`.env`, mode 600), outside frontend variables, tool environments, logs and tracked files. Keep generated build, test and runtime data ignored.
- Preserve user data, Runner identities and journals. Use append-only migrations for existing team data; never clear a database to fix an error. Regenerate sqlc source with SQL changes.
- Preserve explicit execution authorization, frozen inputs, current permissions, immutable originals and exact file versions. Personal editing drafts are private; collaboration drafts are team-visible. Shared publication, human approval and delivery are distinct actions.
- Give each state lifecycle one owner. Preserve unsaved work and stable request IDs after failed or uncertain writes; never display them as saved. Check asynchronous ordering, cancellation and recovery.
- For UI changes, follow the [review checklist](docs/design.md#review-checklist), including keyboard, selection/IME, pointer, touch, narrow layouts and direct inline PR comparison images. Keep blocking errors and recovery visible.
- Run relevant checks and report what actually passed or skipped. Source, browser, fixture, native isolation and real-provider checks establish different guarantees.

Update callers, tests and owning contracts together when behavior changes. Keep README focused on the product and quick start, user workflow details in [Team workflow](docs/workflow.md), and development/check commands in CONTRIBUTING. Keep detailed rules and design rationale in their owning contracts. Track open work in issues and [implementation limits](docs/architecture.md#current-implementation-limits); retain acceptance evidence with [releases or CI](docs/install.md#release-evidence), rather than adding completed plans or private preview records to guides.
