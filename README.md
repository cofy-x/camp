# Camp

Camp is a trusted team's Agent workspace with a Platinum browser desktop. One Server owns team discussion and versioned files; independent Linux Runners execute Agent work. Members use shared Agents from a browser. A project import or programming language manifest is not required to discuss or draft a document.

Start development at [AGENTS.md](AGENTS.md). The long-term contracts are [architecture](docs/architecture.md), [API](docs/api.md), [design](docs/design.md), [execution](docs/dsh.md) and [installation](docs/install.md).

Camp is under active development. See [installation](docs/install.md#supported-deployment) for supported deployments, [contributing](CONTRIBUTING.md) for issues and pull requests, and [security](SECURITY.md) for vulnerability reporting.

## Team workflow

The five applications support **collect sources → Room discussion → Agent execution or collaboration → read results → human review → versioned delivery**:

| Application | Responsibility |
| --- | --- |
| Workspace | Rooms, Threads, explicit Agent requests, collaborations and results |
| Library | Immutable originals, shared working files, private text drafts and history |
| Agents | Identities, responsibilities, file access, Runner targets and Room sharing |
| Runners | Owned/shared machines, connection, execution readiness and grants |
| Settings | Team, invitations, model configuration, storage and appearance |

1. Discuss in a Room. Ordinary messages start no work; select an Agent or mention its handle for one execution. **Post only** suppresses Agent execution.
2. Attach files or select Library sources. Uploading saves originals; **Add to Message** prepares an unsent, version-pinned reference. PDF/DOCX text prepares without a model call; unavailable text retains the original download. See [file and document contracts](docs/api.md#uploads-and-documents).
3. Send explicitly, then read the result and exact saved files in the conversation. Check status describes configured commands, not business acceptance. **Ask agent…** prepares a cited follow-up that still needs a recipient and Send.
4. Open a shared file in Library and **Request Review…** from a human. Requested changes, resubmission, approval and delivery use the same review Thread. Editing autosaves privately; **Publish Changes** creates a shared version.
5. Deliver the approved current version explicitly. Delivered bytes remain pinned after later edits. History and restore preserve earlier versions.

**Start collaboration** separately authorizes a goal, participants and a finite execution allowance. Agents can hand off, ask a person and finish. They work in a separate draft; final publication compares the starting shared version. Conflicts retain the draft and require explicit continuation. Work continues after closing the browser and remains separate from human review.

All Rooms, shared files and collaboration task drafts are team-visible. Task drafts remain separate from shared Room files until final publication. Personal editing drafts, Inbox, reads and desktop layouts belong to each member. Agent sharing delegates invocation; machine administration and source import still require owner/admin authority.

## Local development

Use Go **1.27.3**, Node **24.16.0** and pnpm **12.11.2**, as pinned in [go.mod](go.mod), [.node-version](.node-version) and [package.json](package.json). Run commands from this repository root. The Makefile invokes fixed pnpm via `npx`.

```sh
git clone https://github.com/cofy-x/camp.git
cd camp
make install
DATA_DIR="$PWD/.data/dev" make dev
```

Open `http://127.0.0.1:5173`. Vite proxies `/api` to Go on `127.0.0.1:3001`; restart development after Go changes. `PORT`, `WEB_PORT` and `DATA_DIR` select independent listeners and storage. Always set a project-owned development data directory; the bare CLI otherwise defaults to the OS user configuration directory.

First startup writes `team-setup.token` inside that data directory. Enter it privately in the setup screen, create the administrator and configure Settings. Keep private model values in the ignored root `.env` with mode 600, using [.env.example](.env.example) as the key reference. See [configuration and budgets](docs/dsh.md#configuration-and-budgets).

The Server requires Git for file storage. Discussion and plain-text file work need no model or native execution runtime. Optional PDF reading uses the [document runtime](tools/document-runtime/README.md). Agent execution requires a registered Runner; follow [source Runner setup](docs/dsh.md#source-runner-setup) or use the [packaged installer](docs/install.md). No service starts automatically.

## Build

```sh
make build
./dist/camp serve --addr 127.0.0.1:3001 --data-dir "$PWD/.data/dev"
```

The executable embeds Web/API; development and ordinary Go tests do not need generated frontend assets. [GitHub Releases](https://github.com/cofy-x/camp/releases) distribute fixed Server/Runner packages, checksums and acceptance evidence; [GHCR](https://github.com/cofy-x/camp/pkgs/container/camp) distributes the matching Server image. Choose an explicit version and follow [installation](docs/install.md), or see [release building](docs/install.md#build-and-publish-a-release). Browser host Terminal and executable site previews are disabled.

## Checks and maintenance

Choose checks for the affected contract. These commands describe available checks, not a record of a completed run.

On Linux, ordinary Go tests require Bubblewrap (`bwrap`) on PATH to validate execution command construction. Install it through the host package manager before running those tests. Native integration cases have additional prerequisites and remain opt-in.

| Command | Scope and prerequisites |
| --- | --- |
| `make test-go-race` | SQL generation consistency and Go tests with race detection; opt-in native cases can skip |
| `make typecheck lint format-check` | TypeScript, ESLint, Go vet/gofmt and Prettier |
| `make check-docs` | Local Markdown links/heading anchors and documented toolchain/runtime versions against their pins; no network requests |
| `make test` | Documentation consistency, SQL generation, ordinary Go tests, script and frontend unit tests |
| `make test-e2e` | Production-built UI with temporary keyless data; Chromium by default |
| `make build smoke-production` | Embedded production binary, API/authentication and restart smoke |
| `make test-layerfs test-dsh test-project-env` | Opt-in native checks; install fixed runtimes and Linux host prerequisites first |
| `make test-remote` | Two fixture Runners over TLS/Compose; Docker required |
| `make test-remote-native` | Actual remote DSH/LayerFS path; Linux native prerequisites |
| `make package-daemon smoke-bundle` | Relocated online/offline Runner acceptance; installed release runtimes and native host required |

For the four-project browser matrix:

```sh
npx --yes pnpm@12.11.2 exec playwright install chromium firefox webkit
make test-e2e E2E_ARGS="--project=chromium --project=firefox --project=webkit --project=touch"
```

`E2E_PORT` overrides the test listener (default 13009); the runner refuses an occupied port. Reports and screenshots remain ignored. See [remote validation](tools/remote-dev/README.md) and the [UI review checklist](docs/design.md#review-checklist) for their distinct scopes. Native skips are not passes. Real-provider checks require an explicit allowance and are described in [execution](docs/dsh.md#real-provider-verification).

SQL migrations and named queries live in `apps/server/internal/storage`. After changing them, run `make generate` and `make generate-check`; maintain generated Go output with those inputs. Use `make format` or `make lint-fix-format` for formatting and safe lint fixes. Never reset existing data to repair a migration.

`make check-docs` includes new, unignored Markdown files before staging. It checks local links and headings, current guides' explicit Go/Node/pnpm/DSH/PDF.js version labels, `pnpm@...` commands and matching toolchain declarations in manifests. Historical decision records under `docs/decisions/` receive link checks only. External URLs, runtime behavior and unlabelled version numbers need separate review. Keep formal verification results with [release evidence](docs/install.md#release-evidence).

## License

Camp is licensed under the [MIT License](LICENSE). Bundled dependencies and [typefaces](packages/platinum/src/fonts/README.md) retain their own licenses and notices.
