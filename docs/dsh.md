# DSH and LayerFS execution

The [native Runner design and acceptance contract](native-runner.md) separates sandbox, workspace, process and checkpoint ownership. Source supports Linux LayerFS execution and Mac ARM64 native local workspaces; published package support follows the exact release's [installation guide](install.md).

DSH owns model sessions and the model/tool loop. Camp Server owns admission, permissions, model credentials, durable scheduling and publication. Runner owns execution authorization, native state, cleanup and retained transport results. Workspace providers own rebuildable files and capture. Server and Runner remain separate processes even on one host; all Agent work uses the remote protocol. Linux uses LayerFS; the Mac local provider requires no Linux environment.

## Public integration

The maintained pins are in the [DSH manifest](../tools/dsh-runtime/package.json), its lockfile and [LayerFS pin](../tools/layerfs-runtime/pin.json). Runtime installation uses private Camp directories and public APIs, never modifies shared checkouts. [DSH wire lifecycle](../tools/dsh-runtime/README.md), [Camp tool protocol](../tools/dsh-runtime/camp-tools/README.md), [LayerFS bridge](../tools/layerfs-runtime/README.md) and [sandbox helper](../apps/server/internal/sandboxexec/README.md) own adapter details. The [workspace contract](../apps/server/internal/workspace/workspace.go) and [tool backend](../apps/server/internal/runnertools/backend.go) separate those implementations from the shared execution loop and Runner journal/publication ownership.

Each Run starts a separate DSH process using `initialize`, `session/resume`, `session/prompt`, `session/wait` and `shutdown`. The [production overlay](../apps/server/internal/dsh/remote_overlay.go) freezes persona, replaces filesystem/shell providers, and disables persistent/background shells, image tools and automatic model retry. Ordinary requests expose no collaboration coordination tools.

On Linux, DSH runs inside Bubblewrap with private sessions/configuration and an empty read-only `/workspace` placeholder. File/command tools call a random Run-scoped loopback capability endpoint. The private Node bridge uses public LayerFS SDK entrypoints; Go pins each Sandbox to its full Docker container ID. Commands see `/workspace` and exact read-only evidence under `/references`, without model keys, proxy credentials, Docker control, Git authority or other sessions.

On macOS 15+ ARM64, the unprivileged provider creates an owned local copy, using APFS clones when supported. Its stable session namespace preserves DSH's stored directory while each request receives fresh frozen input. File tools and commands use the same actual native paths. DSH can read that authorized workspace, its pinned runtime and private state; writes go through the capability service. Its Seatbelt profile permits only IPv4 TCP connections to the attempt's private control ports. A Go relay retains the Server model capability and TLS trust. Commands and configured checks have no network or DSH/model capability. Read-only Agents cannot run commands or mutate files.

The private Mac DSH state directory reserves an owner-only `package.json` containing the fixed private ES module scope. This stops SDK plugin discovery from consulting an unauthorized ancestor checkout. Create it exclusively and retain incompatible existing contents with an explicit error; do not overwrite session data or grant ancestor access. The optional [Linux checkpoint worker](layerfs-worker.md) changes checkpoint ownership only; commands, checks, authorization and model access retain these Mac boundaries.

Read-only Agents have structured read/list/search capabilities and no shell/write endpoint. This is a capability boundary; the underlying FUSE mount is writable. Writable commands run as a nonroot user under the trusted helper. Its subreaper, process identities, framed output and seccomp policy supervise descendants and deny IP sockets. Unknown cleanup makes the entire frozen container unusable; main-process exit alone is insufficient. Commands must be quiescent before checks, checkpoint, export and publication.

Mac commands and DSH each have an independently owned launchd resource coalition. The trusted process owner observes kernel PID generations and confirms cleanup even after detached children or client death. Seatbelt grants literal roots and the ancestor metadata needed to traverse OS aliases; listing an owned directory does not grant reads of its children. Unknown cleanup blocks capture/publication and suspends the Runner. Captured regular files use the existing binary/mode/path/size policy. Interrupted native copies are retained under `native-recovery` after startup proves quiescence; recovery never publishes them or replays a prompt.

## Environment and persistence

Resource environment configuration is optional and does not gate ordinary files. [Project preparation](../tools/project-runtime/README.md) installs locked Node dependencies with scripts/hooks disabled in a separate trusted network-enabled stage. Production copies those dependencies into the LayerFS seed; model commands and configured final checks run in that Sandbox without network access. Arbitrary language toolchains are not automatically installed. Missing tools fail the operation that needs them. Exact check coverage and status are validated by Server.

The bounded exporter returns an archive for Go path/type/size/digest validation. Only confirmed captured bytes can be retained after failure. Shared publication uses immutable Git and a final SQLite transaction; LayerFS's Disposable Store cannot recover a lost delivery. Prepared LayerFS projects are a bounded cache, retired according to [native cache policy](../apps/server/internal/nativeexecutor/cache.go) because the pinned SDK has no branch-deletion operation. Session logs have a separate private lifetime from temporary overlays, seed copies and upload archives.

Native resume requires acknowledged native completion, orderly process exit and accepted Camp completion with matching Runner/storage/runtime/generation. Crash, cancellation or missing state invalidates it. The next explicit request rebuilds bounded visible context and announces that fact; it never replays an uncertain prompt. Startup cleans only the daemon's owned runtime home. Uncertain cleanup suspends that Runner while other Runners remain usable.

## Configuration and budgets

Server model precedence is **nonempty process environment → selected private file → managed Settings → defaults**. File selection is `serve --env-file` → `CAMP_ENV_FILE` → working-directory `.env`. A missing default file is allowed; a missing explicitly selected file fails. Parsing uses an allowlist without shell execution or interpolation; [.env.example](../.env.example) lists accepted keys. [CLI resolution](../apps/server/internal/cli/dsh.go) and [managed settings](../apps/server/internal/configuration/models.go) are authoritative.

Keep the file mode 600. Keys are write-only in Settings and stay on Server. Queued work freezes an opaque private revision that can resolve old credentials after restart. Settings reports external overrides and cannot overwrite them. A configured key does not prove connectivity; observed provider responses are a separate fact.

| Setting                         | Default | Maximum |
| ------------------------------- | ------- | ------- |
| Output tokens per model request | 4096    | 8192    |
| Observed model steps per Run    | 8       | 32      |
| Execution timeout in seconds    | 180     | 900     |
| Concurrent Server Runs          | 2       | 64      |

`CAMP_MAX_CONCURRENT_RUNS` or `serve --max-concurrent-runs` sets Server concurrency. Runner `setup --slots` independently selects its capacity (default one). Each Agent executes serially. These controls bound execution, not exact monetary cost. Collaboration request/round allowances are specified once in the [task API](api.md#collaborations); each upstream request is reserved before sending, including unknown outcomes.

The Runner receives an expiring, revocable **Attempt-scoped** capability for the Server's HTTPS Messages proxy. It cannot choose an upstream URL. Server resolves the frozen model revision, validates authority/budget and never transparently retries a transmitted request or follows provider redirects. Real keys never reach the Runner. DSH receives only that capability; tools receive neither. A custom Server CA is mounted privately and passed via `NODE_EXTRA_CA_CERTS`; TLS verification stays enabled.

A complete SSE `message_stop` durably ends one request before forwarding its final frame, allowing the next step even if the provider keeps the connection open. Incomplete EOF interrupts the Attempt. WSS control loss and model HTTPS have separate lifetimes within the authorization lease. Server restart interrupts in-flight model streams; daemon restart can upload only fully retained results. Safe diagnostic classifications exclude raw credential-bearing provider errors.

## Source Runner setup

This is a development workflow; members use the [packaged installer](install.md). Run from the Camp repository root on a host meeting its native prerequisites:

```sh
make install-dsh install-project-runtime
# Linux LayerFS backend only:
make install-layerfs
make build-sandbox
make build
./dist/camp runner setup --development-runtime \
  --server http://127.0.0.1:3001 --name dev-runner \
  --data-dir "$PWD/.data/runner-dev" --code-file /private/connect-code
./dist/camp runner run --data-dir "$PWD/.data/runner-dev"
```

Create the code in Runners and keep its file private. `--development-runtime` is required for an unbundled source binary. On macOS 15+ ARM64, skip `make install-layerfs`, use the original unprivileged account, and optionally provide explicit `--tool-root` directories as described in the [native operator contract](native-runner.md#operator-setup-and-lifecycle). Linux uses the privileged account required by LayerFS. Runner runtime paths come from process variables (`CAMP_DSH_RUNTIME`, `CAMP_LAYERFS_RUNTIME`, `CAMP_SANDBOX_HELPER`, `CAMP_DSH_NODE`) or source-relative defaults, not Server's private `.env`; keep the working directory or provide absolute paths. `CAMP_DOCKER_SOCKET` chooses the Linux local socket. No model key belongs in Runner configuration.

`CAMP_DAEMON_DATA_DIR` optionally lets `make dev` start an already registered local Runner with the same development Server origin. It does not enroll or install a service. [Installation](install.md) owns packaged cache repair and explicit service lifecycle.

## Deterministic native checks

After installing the fixed runtimes and building the helper:

```sh
make test-layerfs test-dsh test-project-env
make test-remote-native
CAMP_TEST_DOCUMENT_RUNTIME="$PWD/tools/document-runtime" \
  go test -race ./apps/server/internal/documenttext -count=1
```

The parser command additionally needs `make install-document-runtime`. These checks use real native processes with deterministic local providers, not a paid model key. Missing prerequisites can skip gated Go tests; inspect skips rather than treating a zero exit as native acceptance. See [contribution checks](../CONTRIBUTING.md#checks) for source/browser checks and [remote validation](../tools/remote-dev/README.md) for Compose Fixture scope.

## Collaboration decisions

Only task rounds install `camp_handoff`, `camp_ask_person` and `camp_finish` using the public DSH tool registration and `concludeTurn()` API. The separate private coordinator validates approved participants and relative artifact paths, seals and drains workspace tools, and journals one call-ID/body decision before acknowledging it. Identical replays return that acknowledgement; another decision conflicts. The pre-step guard consumes queued context after conclusion without another model request. Ordinary `@` output cannot dispatch work.

Handoff/question rounds retain an unpublished checkpoint and defer final checks. A writer finish runs configured checks before export/cleanup. Server validates the complete result before committing a checkpoint and any successor. Intent alone cannot restart execution after a crash. Adapter modules participate in the runtime fingerprint, so changes invalidate incompatible native sessions. Final shared publication and human review follow [architecture](architecture.md#collaboration-tasks).

## Real-provider verification

These are separately authorized acceptance tools, never routine tests. With Server, model configuration and ready native Runner(s) already running, choose the intended budgeted scenario:

```sh
node scripts/verify-remote-real.mjs --allow-two-real-runs \
  https://camp.example.com /private/access.json RUNNER_ID /private/new-remote-report
node scripts/verify-collaboration-real.mjs --allow-one-real-task \
  https://camp.example.com /private/access.json RUNNER_A RUNNER_B /private/new-task-report
```

The first submits two Runs (create/revise), checks native resume and human review/resubmission/delivery through HTTP. The second authorizes one bounded Writer → read-only Reviewer → Writer collaboration across distinct native Runners, then verifies exact publication and human delivery. Neither is browser acceptance.

The access format is `{"origin":"https://camp.example.com","admin":{"username":"...","password":"..."}}`. Use an enabled administrator and a small owner-only file (mode 600 on Unix). Both tools require the effective per-Run model limits at or below the defaults in this guide; they do not change settings. Keep configuration stable during a run. Use `NODE_EXTRA_CA_CERTS` for a private CA rather than disabling verification.

Report/ledger identities are reserved durably before work submission. Unknown outcomes consume the authorization; commands refuse reused reports and never retry work automatically. Inspect saved request/Room/Run/Task IDs after failure and use explicit cancellation if needed. A new report path grants new work; it is not a recovery instruction. Keep reports private.

[The browser demonstration](../scripts/demo-remote-ui.mjs) provides the separately flagged two-Run UI scenario, source prerequisites and private screenshot/video output. The older `verify-rooms-real`, `verify-handoff-real` and `verify-team-real` scripts target retired local workflows and are not current remote acceptance commands. Keeping those source files does not establish compatibility.
