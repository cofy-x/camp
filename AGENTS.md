# Working on Camp

Read the contract for the area you change:

| Document | Owns |
| --- | --- |
| [README](README.md) | Product workflow, local development and checks |
| [Architecture](docs/architecture.md) | Code placement, state ownership, persistence and recovery |
| [API](docs/api.md) | Authentication, permissions, requests and version contracts |
| [Design](docs/design.md) | Platinum UI, content hierarchy and interaction review |
| [Execution](docs/dsh.md) | DSH/LayerFS integration, configuration and model limits |
| [Installation](docs/install.md) | Server/Runner deployment, releases and backup |

- Inspect this repository's Git status before editing; preserve existing changes. Shared checkouts are read-only references. Pin runtime dependencies here and use their public APIs.
- Follow the pinned toolchains, package boundaries and configured formatters; keep version files, manifests and lockfiles consistent. Keep code, comments, product text and documentation in English; preserve user data and external output as authored.
- Put reusable desktop controls in `packages/platinum` and application behavior in `apps/dashboard/src/features`. Storage owns persistent business invariants and atomic state changes; protocol parsing, archive safety and execution checks belong to their respective modules. Follow the [package boundaries](docs/architecture.md#package-boundaries). Register apps with `AppDefinition`; use shared desktop navigation, dialogs, menus and close guards.
- Avoid unnecessary secondary labels and descriptions. Let the layout do the explaining. Follow the [content hierarchy](docs/design.md#content-and-information-hierarchy); keep blocking errors, permission consequences and recovery visible.
- Keep secrets in private server configuration (`.env`, mode 600, ignored). Never expose keys through frontend variables, tool environments, logs or tracked files. Keep generated build/test/runtime data ignored; maintain lockfiles and generated sqlc source.
- Preserve explicit execution authorization, frozen inputs, current permissions and exact file versions. Originals are immutable; personal editing drafts are private, while collaboration task drafts are team-visible. Shared publication, human approval and delivery are distinct actions.
- Give each state lifecycle one owner. Preserve drafts and live work across navigation, hiding and shading. Failed or uncertain writes retain local work and stable request IDs; never display them as saved.
- Keep schema/queries in SQL and regenerate sqlc output with its inputs. Use append-only migrations for existing team data; never clear a database to fix an error. Breaking API changes may remove discarded development compatibility; update callers, tests and contracts together.
- Check ordering, failure, cancellation and recovery for asynchronous work. UI changes also require keyboard, text selection/IME, pointer, touch and narrow-window review. Report actual checks and their limits: source, browser, native runtime and real model evidence are separate.

Keep enduring constraints in their owning document, with links to implementation where useful. Distinguish current implementation boundaries from [open work and its acceptance conditions](docs/architecture.md#current-implementation-limits). Update contracts when behavior changes and run `make check-docs`. For a significant cross-module decision, retain a short rationale, alternatives and consequences in the owning document; use a linked decision record only when needed.

Keep completed plans and private preview records out of the maintained guides. Preserve formal acceptance evidence with the release or retained CI artifacts under the [release evidence policy](docs/install.md#release-evidence), rather than accumulating dated reports in source.
