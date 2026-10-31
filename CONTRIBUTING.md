# Contributing to Camp

Follow the [cofy-x contribution guidelines](https://github.com/cofy-x/.github/blob/main/CONTRIBUTING.md). Use [Issues](https://github.com/cofy-x/camp/issues) for reproducible bugs and focused proposals, and [SECURITY.md](SECURITY.md) for possible vulnerabilities. Discuss changes to execution authorization, persistence, public APIs or deployment boundaries before building a large change.

## Development

Fork the repository and create a branch for your change. Read [AGENTS.md](AGENTS.md) for repository constraints and the contracts for the area you change. A source checkout can run the Server without native Runner dependencies or model credentials.

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

Shared dependency checkouts are read-only references. Integrate through public interfaces and pinned dependencies; keep their branch, index and files intact.

## Build

```sh
make build
./dist/camp serve --addr 127.0.0.1:3001 --data-dir "$PWD/.data/dev"
```

The executable embeds Web/API; development and ordinary Go tests do not need generated frontend assets. Server/Runner packages and the Server image have a separate [release build and publication guide](docs/install.md#build-and-publish-a-release).

## Checks

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

`make check-docs` includes new, unignored Markdown files before staging. It checks local links and headings, current guides' explicit Go/Node/pnpm/DSH/PDF.js version labels and `pnpm@...` commands against their pins. Historical decision records under `docs/decisions/` receive link checks only. External URLs, runtime behavior and unlabelled version numbers need separate verification.

## Pull requests

Keep changes focused and update the owning documentation when behavior changes. Add tests for meaningful behavior and failure cases. Explain the problem, resulting behavior and relevant tradeoffs. Include the commands you actually ran and their results, including skipped cases.

UI changes should include reviewed before/after images using synthetic data, embedded directly as GitHub image attachments. Follow the interactions in the [design review checklist](docs/design.md#review-checklist). Keep secrets, personal team data and generated runtime files out of patches and public attachments. Retain formal acceptance under the [release evidence policy](docs/install.md#release-evidence).

The [CI workflow](.github/workflows/ci.yml) runs these checks:

| Job | Scope |
| --- | --- |
| Source | Formatting, types, lint, documentation links, generated SQL, Go race tests, script/frontend unit tests, production build and smoke |
| Browser | Production UI with fresh keyless data in Chromium, Firefox, WebKit and touch projects |
| Fixture | TLS/Compose with two fixture Runners, collaboration, publication, cancellation, restart and reconnect |
| Server / darwin-arm64 and darwin-amd64 | Actual Mac builds, launcher race tests, authentication/file/restart smoke, WebKit authentication/discussion/file checks and fixed-release Homebrew installation |

Run relevant checks before opening a pull request. CI uses a read-only GitHub token and fresh test data without model credentials. Native Runner isolation, privileged host cleanup, packaged Runner installation and real-provider acceptance have separate checks under [execution](docs/dsh.md#real-provider-verification), [native Runner acceptance](docs/native-runner.md#evidence-and-acceptance) and [release evidence](docs/install.md#release-evidence).

Third-party code and assets retain their own licenses and notices. Preserve those notices when changing dependencies or distribution contents; see [license information](README.md#license).

## Mac checkpoint worker

The standalone [Mac checkpoint adapter](tools/layerfs-worker/README.md) is experimental. On an actual Apple Silicon Mac with an existing native ARM64 Docker engine, build the fixed Linux addon/daemon/image independently of the Linux amd64 Runner:

```sh
node tools/layerfs-worker/install.mjs --source /absolute/path/to/read-only/layerfs
node --test tools/layerfs-worker/test/*.test.mjs
node tools/layerfs-worker/qualify.mjs .data/worker-arm64-qualification.json
node tools/layerfs-worker/qualify-mac.mjs
```

The supplied checkout must contain the exact Camp LayerFS pin; it is only read through `git archive`. The Linux probe checks actual architecture, Linux-local Store placement, FUSE, Commit and remount. The Mac probe separately captures a complete synthetic working tree, streams it to the selected engine, checks three generations and restores actual checkpoint bytes to a fresh Mac directory. Its report states its actual scope. Ordinary unit tests or this standalone probe do not replace native Runner isolation, real interruption/restart cases or three-round comparative measurements required by [the worker contract](docs/layerfs-worker.md).

Omitting `--source` fetches the exact public revision into Camp's private build context. The installer checks the actual Linux engine architecture and collects ARM64 Rust and pinned Docker CLI notices. It does not build in the shared checkout or publish an image.

Additional actual-Mac qualification commands use new private fixtures and deterministic providers:

```sh
node tools/layerfs-worker/qualify-faults.mjs
node tools/layerfs-worker/qualify-recovery.mjs
node tools/layerfs-worker/qualify-upstream.mjs
node tools/layerfs-worker/benchmark.mjs
```

The benchmark retains three rounds on the selected running engine, rotates transport order and measures a common actual seal separately from each transport. It also writes `native-plan.json`; run `TestNativeLocalComparisonMeasurements` with `CAMP_MAC_COMPARISON_PLAN` set to that file and `CAMP_MAC_COMPARISON_OUTPUT` set to a new private JSON filename. This measures production native-local Prepare/publication on the identical inputs. The full-tree local profile is a separate comparison, not a claim of production native-local history/restore. Preserve failed and superseded samples, describe cache/background activity and never label an existing VM a cold VM start.

For the affected native boundary/provider tests, supply absolute `CAMP_TEST_SANDBOX_HELPER`, `CAMP_TEST_DSH_RUNTIME`, `CAMP_TEST_LAYERFS_WORKER_RUNTIME` and `CAMP_TEST_DOCKER_SOCKET`, then run the native executor tests with race detection. `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker` selects the hybrid in the deterministic TLS/provider test; omitting it retains the native-local comparison.

Build an optional worker-enabled installation candidate without publishing:

```sh
node scripts/package-worker.mjs
node scripts/package-runner.mjs --checkpoint-worker
node scripts/build.mjs
```

The matching image descriptor, source and helper hashes are checked before packaging. The ordinary package command remains available without worker support. The release workflow builds the optional image on native Linux ARM64, transfers only its fixed artifact/metadata to the Mac package builder, and stages its checksum with the release. Linux artifact qualification does not replace the actual Mac hybrid acceptance.

Run `scripts/smoke-bundle.mjs online` and `offline` first with the native-local default, then with `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker`, `CAMP_TEST_DOCKER_BINARY` set to the selected absolute Docker executable, `CAMP_TEST_DOCKER_SOCKET`, and `CAMP_TEST_WORKER_IMAGE` set to the matching optional image archive. The hybrid fixture additionally kills/restarts the actual packaged Runner, switches stopped backends, copies its stopped private backup, restores actual native/worker checkpoint bytes and verifies no model replay. It retains its private fixture and controlled identity for inspection. These are local candidate/mirror checks; public download and cross-version release acceptance remain distinct.
