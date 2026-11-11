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

The Server requires Git for file storage. Discussion, Markdown and plain-text file work need no model or native execution runtime. Markdown is rendered directly; other attachments retain their original bytes and readable UTF-8 files can be viewed as source. Agent execution requires a registered Runner; follow [source Runner setup](docs/dsh.md#source-runner-setup) or the [packaged installer](docs/install.md). [Installation](docs/install.md#supported-deployment) owns platform support and host prerequisites. No service starts automatically.

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

Source CI runs static checks, SQL generation consistency, script/frontend tests, production verification and two Go race-test shards independently. The Go shards discover all test/example/fuzz names from the actual race-enabled package build and partition top-level names; nested tests and fuzz seeds remain together. Both shards are required, while `make test-go-race` still runs the complete suite locally. To reproduce a CI shard, use `make test-go-shard GO_TEST_SHARD=1/2` or `GO_TEST_SHARD=2/2`; SQL consistency remains a separate required CI check.

Release-only portable Mac source qualification uses the same name partitioner over its exact portable package set, retaining the single documented Linux-only exclusion and package parallelism of two. It discovers names with race detection enabled, then executes both complementary shards sequentially with `-count=1`; every nested test, example and fuzz seed stays with its top-level name. Both shards must pass, with Go's default per-package timeout and the existing job deadline unchanged. Run `node scripts/check-portable-go.mjs` on Mac to reproduce the complete qualification.

Every browser project runs both file shards with one worker and an isolated temporary Server/database per job. Add `--shard=1/2` or `--shard=2/2` to `E2E_ARGS` to reproduce one. Evidence uploads remain strict; a separate cancelable job verifies all eight successful shards and exactly one successful upload of each project's keyboard, preview and review evidence, including reused successful jobs after a failed-job re-run. The protected Browser gate requires this evidence check as well as the test matrix. Fixture and checkpoint caches only reuse build inputs and artifacts; all qualification still executes on both cache hits and misses. Main pushes populate caches automatically within the existing cache quota; PRs only read them.

Linux browser CI uses the official Playwright Noble image pinned by version and registry digest. Its version must match the exact `@playwright/test` dependency; update both together and qualify all projects. The image supplies browsers and their system libraries, while frozen project installation, the production Server build, every test shard and evidence collection still run. CI invokes `test:e2e` directly; local `make test-e2e` continues to install requested browsers. Browser containers do not establish host-native Runner or FUSE acceptance.

Linux host jobs in source, checkpoint and release workflows use [one dependency installer](scripts/install-linux-deps.mjs). It selects signed official Ubuntu archives for the actual amd64/arm64 architecture through a temporary source file, retaining release, updates, backports and security pockets without modifying hosted source files or contacting unrelated package repositories. Index refresh fails on any error before installation can use stale metadata. Acquisition has two retries and 15-second transfer timeouts; update and installation have 120/180-second deadlines inside a six-minute workflow step. On failure, source/image details, resolver configuration and bounded DNS lookups accompany the original failure status. Tests and native checks are never retried or skipped by this installer. Hosted network/DNS failure can still fail a job; source selection and bounded retries do not guarantee network availability.

Before merging cache changes, maintainers can manually qualify a branch with `gh workflow run ci.yml --ref BRANCH -f populate_build_cache=true`, then repeat with `populate_build_cache=false` to verify restoration. Both runs execute complete validation. The explicit option only enables cache writes for a manual dispatch, using GitHub's branch-scoped cache permissions; it cannot enable writes from a PR or release caller. Checkpoint qualification also exposes this option when dispatched directly.

Native Mac CI measures dependency installation, embedded builds, Runner packaging and online/offline acceptance separately on both existing macOS versions. Compiler outputs and pinned dependency directories share a build cache keyed by the actual sandbox helper dependencies, runtime sources, toolchains and hosted image. Frozen installs, every native test and the retained operator qualification still execute on a hit. Successful main or explicit manual qualification can populate this cache; native qualification exposes the same manual option as checkpoint qualification.

Runtime packaging can reuse compression with `CAMP_RUNTIME_ARCHIVE_CACHE=/owned/cache/path`. The packager still assembles and inventories the current runtime. Before reuse, the archive tool hashes its complete normalized tar stream and compares the decompressed cached stream, including bytes, executable modes, directories and symlinks; changed or corrupt inputs regenerate the archive. Installer compilation, catalogs and checksums remain current. Online and offline acceptance run sequentially on each host, avoiding contention during complete native installation checks. Each scenario has fresh independent installations, databases, ports and launchd identities. Both complete scenarios, their archive/source-bound reports, all native checks and cleanup must pass. Timing and installation evidence remain in the existing native artifacts; runtime homes, journals and test results are not restored as validation.

Reuse local results only for identical actual inputs, tool versions and validation scope; record base, commit/tree, executed/skipped checks and any original reused receipt. Relevant validator/tool changes invalidate affected results. CI owns PR/main integration, and release owns complete distribution acceptance.

On Linux, ordinary Go tests require Bubblewrap (`bwrap`) on PATH to validate execution command construction. Install it through the host package manager before running those tests. Native integration cases have additional prerequisites and remain opt-in.

| Command | Scope and prerequisites |
| --- | --- |
| `node --test scripts/change-policy.test.mjs scripts/ci-policy.test.mjs` | Change classification, protected-check behavior and release source policy; Node only |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` | Workflow syntax and reusable-job contracts; fixed tool version |
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
| `make build package-runner smoke-bundle` | Host-native Runner packages and relocated online/offline acceptance on Linux amd64 or ARM64 Mac; installed native runtimes required |
| `make package-daemon` | Complete Linux Runner and Server development packages; Linux amd64 native host required |

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

`make check-docs` checks new/unignored Markdown, local links/anchors and documented Go/Node/pnpm/DSH versions against their pins. Historical `docs/decisions/` receive link checks only. External URLs, behavior and unlabelled versions need source comparison. Run the pinned formatter on changed Markdown; documentation-only work needs no new native or paid-provider campaign.

## Pull requests

Keep changes focused and update the owning documentation when behavior changes. Add tests for meaningful behavior and failure cases. Explain the problem, resulting behavior and relevant tradeoffs. Include the commands you actually ran and their results, including skipped cases.

Use Conventional Commits for PR titles and new commit messages: `type(scope): summary`, with an optional scope. Choose `feat`, `fix`, `refactor`, `perf`, `docs`, `test`, `build`, `ci`, `chore` or `revert` according to the primary change. For example, `feat(runner): add task input history reads` or `fix: preserve unsaved Library edits`. Keep the summary concise and describe the final change. Mark breaking changes with `!` before the colon and explain their impact in the PR description.

Camp uses squash merges with the PR title as the commit subject. Verify the PR title and final squash subject before merging. Correct merged PR titles through GitHub metadata; do not rewrite published main history just to normalize old messages. Preserve release tags and their recorded commit identities.

UI changes should include reviewed before/after images using synthetic data, embedded directly as GitHub image attachments. Follow the interactions in the [design review checklist](docs/design.md#review-checklist). Keep secrets, personal team data and generated runtime files out of patches and public attachments. Retain formal acceptance under the [release evidence policy](docs/releasing.md#release-evidence).

The [CI workflow](.github/workflows/ci.yml) owns the current source/browser/fixture and Mac Server job matrix. [Native Mac qualification](.github/workflows/native-mac.yml), [Linux checkpoint qualification](.github/workflows/checkpoint-proof.yml) and [release verification](docs/releasing.md) establish separate guarantees. CI uses fresh test data without model credentials; source or fixture success does not establish native isolation, public installation or real-provider success.

Third-party code and assets retain their own licenses and notices. Preserve those notices when changing dependencies or distribution contents; see [license information](README.md#license).

## Mac checkpoint worker

The optional [Mac checkpoint tooling](tools/layerfs-worker/README.md#source-builds-and-qualification) owns source installation, actual-host qualification, comparison campaigns and package-restart checks. Read the [worker contract](docs/layerfs-worker.md) before changing capture, transport or recovery. Standalone probes, packaged Runner acceptance and public release verification establish separate guarantees.
