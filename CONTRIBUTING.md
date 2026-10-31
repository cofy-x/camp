# Contributing to Camp

Follow the [cofy-x contribution guidelines](https://github.com/cofy-x/.github/blob/main/CONTRIBUTING.md). Use [Issues](https://github.com/cofy-x/camp/issues) for reproducible bugs and focused proposals, and [SECURITY.md](SECURITY.md) for possible vulnerabilities. Discuss changes to execution authorization, persistence, public APIs or deployment boundaries before building a large change.

## Development

Create a branch for the change and follow any enclosing workspace rules. Read [AGENTS.md](AGENTS.md) and the task's owning contract from the [documentation map](docs/README.md). A source Server needs no native Runner runtime or model credentials.

Use Go **1.27.3**, Node **24.16.0** and pnpm **12.11.2**, as pinned in [go.mod](go.mod), [.node-version](.node-version) and [package.json](package.json). Run commands from the Camp repository root. The Makefile invokes fixed pnpm via `npx`.

```sh
git clone https://github.com/cofy-x/camp.git
cd camp
make install
DATA_DIR="$PWD/.data/dev" make dev
```

Open `http://127.0.0.1:5173`. Vite proxies `/api` to Go on `127.0.0.1:3001`; restart development after Go changes. `PORT`, `WEB_PORT` and `DATA_DIR` select independent listeners and storage. Always set a project-owned development data directory; the bare CLI otherwise defaults to the OS user configuration directory. Keep development worktrees outside runtime `.data/` directories.

First startup writes `team-setup.token` inside that data directory. Enter it privately in the setup screen, create the administrator and configure Settings. Keep private model values in the ignored root `.env` with mode 600, using [.env.example](.env.example) as the key reference. See [configuration and budgets](docs/dsh.md#configuration-and-budgets).

The Server requires Git for file storage. Discussion and plain-text file work need no model or native execution runtime. Optional PDF/DOCX preparation uses the [document runtime](tools/document-runtime/README.md). Agent execution requires a registered Runner; follow [source Runner setup](docs/dsh.md#source-runner-setup) or the [packaged installer](docs/install.md). [Installation](docs/install.md#supported-deployment) owns platform support and host prerequisites. No service starts automatically.

## Build

```sh
make build
./dist/camp serve --addr 127.0.0.1:3001 --data-dir "$PWD/.data/dev"
```

The executable embeds Web/API; development and ordinary Go tests do not need generated frontend assets. Server/Runner packages and the Server image have a separate [release build and publication guide](docs/releasing.md).

## Checks

[The change policy](scripts/change-policy.mjs) classifies actual Git base/head changes, including deletions and both rename paths. CI loads the policy from the trusted base; missing policy, mixed/unknown paths and workflow/policy changes select complete validation. README changes inside runtime/code directories remain documentation. `AGENTS.md` and process-rule edits require review of behavioral effects in addition to automated checks.

Locally, run affected checks first. Documentation needs `make check-docs`, changed-Markdown formatting and content review against its owning implementation. Workflow-policy changes need classifier/gate tests and pinned actionlint; workspace orchestration changes need tests on affected hosts. Use an enclosing workspace's automatic planning/check command when available. These commands describe checks, not completed evidence.

Branch pushes preserve identity, cleanliness, fixed target/branch and fast-forward history; full dual-host regression is not a push gate. Merge CI always resolves protected **Source**, **Browser** and **Fixture** checks, with explicit scope-based skips inside the graph. Uncertain classification fails closed or selects complete checks; cancelled/skipped required work never counts as successful validation. Main protection remains enabled. Formal releases run complete Linux and portable Mac source regression, browser/fixture checks and [native/package/public acceptance](docs/releasing.md), even after documentation-only main CI.

Reuse local results only for identical actual inputs, tool versions and validation scope; record base, commit/tree, executed/skipped checks and any original reused receipt. Relevant validator/tool changes invalidate affected results. CI owns PR/main integration, and release owns complete distribution acceptance.

On Linux, ordinary Go tests require Bubblewrap (`bwrap`) on PATH to validate execution command construction. Install it through the host package manager before running those tests. Native integration cases have additional prerequisites and remain opt-in.

| Command | Scope and prerequisites |
| --- | --- |
| `make test-go-race` | SQL generation consistency and Go tests with race detection; opt-in native cases can skip |
| `make typecheck lint format-check` | TypeScript, ESLint, Go vet/gofmt and Prettier |
| `make check-docs` | Local Markdown links/heading anchors and documented toolchain/runtime versions against their pins; no network requests |
| `make test` | Documentation consistency, SQL generation, ordinary Go tests, script and frontend unit tests |
| `make test-e2e` | Production-built UI with temporary keyless data; Chromium by default |
| `make build smoke-production` | Embedded production binary, API/authentication and restart smoke |
| `node scripts/smoke-upgrade.mjs` | Linux public-baseline Server data upgrade; Go/Git and GitHub access, no model call |
| `make test-layerfs test-dsh test-project-env` | Opt-in native checks; install fixed runtimes and Linux host prerequisites first |
| `make test-remote` | Two fixture Runners over TLS/Compose; Docker required |
| `make test-remote-native` | Actual remote DSH/LayerFS path; Linux native prerequisites |
| `make package-daemon smoke-bundle` | Relocated online/offline Runner acceptance; installed release runtimes and native host required |

For the four-project browser matrix:

```sh
npx --yes pnpm@12.11.2 exec playwright install chromium firefox webkit
make test-e2e E2E_ARGS="--project=chromium --project=firefox --project=webkit --project=touch"
```

`E2E_PORT` overrides the test listener (default 13009); the runner refuses an occupied port. Reports and screenshots remain ignored. Failed CI browser jobs retain reports, screenshots and traces as workflow artifacts. See [remote validation](tools/remote-dev/README.md) and the [UI review checklist](docs/design.md#review-checklist) for their distinct scopes. A native skip is not a pass. Real-provider checks require an explicit allowance under [execution](docs/dsh.md#real-provider-verification).

SQL migrations and named queries live in `apps/server/internal/storage`. After changing them, run `make generate` and `make generate-check`; maintain generated sqlc output with its inputs. Use `make format` or `make lint-fix-format` for formatting and safe lint fixes. Preserve existing data through append-only migrations; never reset a database to repair an upgrade.

## Documentation

Keep each rule or procedure in its owning contract/tool README and link to it elsewhere. README owns quick start; the [documentation map](docs/README.md) routes readers by task. Update incoming links when moving a section. Prefer links to authoritative manifests and code over copying constants or job matrices.

Current guides describe implemented behavior and repeatable procedures. Record unresolved mismatches with an owner and completion condition in [implementation limits](docs/architecture.md#current-implementation-limits). Remove completed plans and superseded instructions; retain dated outcomes through [release evidence](docs/releasing.md#release-evidence).

`make check-docs` checks new/unignored Markdown, local links/anchors and documented Go/Node/pnpm/DSH/PDF.js versions against their pins. Historical `docs/decisions/` receive link checks only. External URLs, behavior and unlabelled versions need source comparison. Run the pinned formatter on changed Markdown; documentation-only work needs no new native or paid-provider campaign.

## Pull requests

Keep changes focused and update the owning documentation when behavior changes. Add tests for meaningful behavior and failure cases. Explain the problem, resulting behavior and relevant tradeoffs. Include the commands you actually ran and their results, including skipped cases.

UI changes should include reviewed before/after images using synthetic data, embedded directly as GitHub image attachments. Follow the interactions in the [design review checklist](docs/design.md#review-checklist). Keep secrets, personal team data and generated runtime files out of patches and public attachments. Retain formal acceptance under the [release evidence policy](docs/releasing.md#release-evidence).

The [CI workflow](.github/workflows/ci.yml) owns the current source/browser/fixture and Mac Server job matrix. [Native Mac qualification](.github/workflows/native-mac.yml), [Linux checkpoint qualification](.github/workflows/checkpoint-proof.yml) and [release verification](docs/releasing.md) establish separate guarantees. CI uses fresh test data without model credentials; source or fixture success does not establish native isolation, public installation or real-provider success.

Third-party code and assets retain their own licenses and notices. Preserve those notices when changing dependencies or distribution contents; see [license information](README.md#license).

## Mac checkpoint worker

The optional [Mac checkpoint tooling](tools/layerfs-worker/README.md#source-builds-and-qualification) owns source installation, actual-host qualification, comparison campaigns and package-restart checks. Read the [worker contract](docs/layerfs-worker.md) before changing capture, transport or recovery. Standalone probes, packaged Runner acceptance and public release verification establish separate guarantees.
