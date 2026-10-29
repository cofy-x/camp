# Camp LayerFS runtime

This directory builds and hosts Camp's private LayerFS SDK bridge. The package, native addon, daemon, and container image use the exact source commit in `pin.json`. Shared checkouts are read through `git archive` and are never built or modified in place. The installer accepts only Linux x64 and the repository's Node version. It uses a private Rust toolchain and Cargo cache.

```sh
make install-project-runtime install-layerfs build-sandbox
node --test tools/layerfs-runtime/test/*.test.mjs
node tools/layerfs-runtime/probe.mjs
CAMP_TEST_LAYERFS_RUNTIME="$PWD/tools/layerfs-runtime" go test -race ./apps/server/internal/layerfs -run TestBridge -count=1
```

Run from the Camp root with the [repository toolchain](../../README.md#local-development). By default the installer shallow-fetches the pinned public revision into Camp's private build cache, filtering large blobs, then archives source without `docs/assets` media. `--source /absolute/path/to/layerfs` can instead archive that exact revision from an existing checkout; inspect that checkout's rules/status first and never build or modify it in place. The installer needs Git, tar, npm, curl, a C linker, Docker, and Camp's installed `tools/project-runtime` with pnpm 12.11.2. It copies the verified pnpm wrapper and native package into the root-owned image; runtime commands never download the package manager. Source builds need access to the pinned Rust toolchain and Cargo dependencies. They are kept under ignored `node_modules/.camp-layerfs`; `installed.json` records artifact hashes and the immutable local Docker image ID. No model credentials are read.

Native Go acceptance can select a dedicated local engine with `CAMP_TEST_DOCKER_SOCKET`; the release CI also selects that engine through `DOCKER_HOST` for build commands. This avoids assuming that a hosted worker's default engine supports the pinned API. Runner operators use `--docker-socket`, as described in the [installation guide](../../docs/install.md).

The installer collects notices for the Linux native dependency graph and Rust toolchain into its private cache; the Runner package retains them under `licenses/rust`. The few published napi-rs crates without license files use the checked-in supplement, matched against each crate's packaged source commit and the original license digest. Changing the LayerFS pin requires reviewing missing notices again; packaging fails when the license inventory is absent.

Runtime requirements are Docker Engine with API v1.54, Linux FUSE, and a local ext4, XFS, or Btrfs Docker volume directory. The trusted host must be able to write that directory and use the Docker socket; ordinary membership in the Docker group alone does not grant the required directory access. On this development host that owner is root. This is a privileged deployment component, not an unprivileged desktop dependency. Nothing installs a persistent service.

The bridge discovers the selected engine's data directory through the public Docker system-info API and verifies that its volume directory is available locally. A custom socket does not imply `/var/lib/docker`; remote engines and inaccessible volume storage remain unsupported.

Startup cleanup queries the public Docker volume API with exact Store/home labels instead of engine-wide disk/image inventory. This keeps unrelated host inventory outside cleanup's output budget. Project removal still uses the public LayerFS runtime API; orphan removal rechecks exact ownership and asks Docker to delete without force. Missing/ambiguous ownership or in-use volumes stop cleanup rather than pruning another Runner's state.

## Boundary and lifetime

Go owns the SDK process, the private runtime home, Run records, resource locks, execution admission, cancellation, export validation, and authoritative result publication. The bridge uses only the public `@layerfs/agent`, `@layerfs/backend`, and `@layerfs/runtime` entrypoints. LayerFS owns its disposable Store, Sandbox, mounted Workspace, and checkpoints.

The Go client writes an owner-readable temporary configuration file, locks the home against another bridge owner, and starts Node with an explicit environment containing no inherited model or shell credentials. The SDK process inherits the home lock and receives SIGKILL if its Linux parent dies. The replacement bridge owner cannot reset while that process still owns the lock, including the interval before the death signal is delivered. DSH runs separately and does not receive the Docker socket or LayerFS administrative APIs. A Run gets one Sandbox and a fork of its frozen resource baseline. Its public Sandbox name is resolved through the Docker API and pinned to a full container ID. UID, GID, and mount path are observed through supported operations, rather than assumed from LayerFS implementation constants.

The command owner must finish every command and reap its descendants before checkpoint, export, or unmount. Camp's trusted supervisor is installed before tools are admitted. It denies IP sockets for model commands while leaving LayerFS's authenticated daemon connection available to the trusted host. The bridge's administrative `workspace.exec` calls are limited to identity observation and archive export; they are never exposed as model tools.

Read-only Agents receive structured read operations, with no shell or write capabilities. The LayerFS mount itself is writable; the read-only boundary is Camp's capability-specific tool endpoint and helper, not a claimed read-only FUSE mount. Binary imports and exports preserve bytes. Returned archives remain untrusted until Go checks entry types, paths, and resource size limits.

## Protocol

`node tools/layerfs-runtime/bridge.mjs /absolute/config.json` accepts one JSON object per line:

```json
{
  "id": "1",
  "method": "workspace.create",
  "params": { "projectId": "p-…", "runId": "run-123", "baseCommit": "main" }
}
```

Each request gets either `{ "id", "result" }` or `{ "id", "error": { "code", "message", "details" } }`. Requests are limited to 64 KiB and executed serially. Operations are never retried automatically.

| Method | Parameters and result |
| --- | --- |
| `runtime.status` | Public runtime readiness, source pin, image ID, reset state, and the Disposable durability limitation. |
| `runtime.reset.begin` | Before project use only: block admission and list this home's public Sandbox metadata. Go stops each running container by its resolved ID. |
| `runtime.reset` | Confirm no running Sandbox, remove private projects, stopped leftovers, owned orphan volumes, and Camp receipts. Any uncertain cleanup leaves admission blocked. |
| `project.prepare` | `sourceDir`, `baselineKey`, optional `environmentKey`; returns `projectId`, image, and whether the exact identity was already prepared. |
| `project.history` | `projectId`; public committed history. |
| `project.remove` | `projectId`; refuses active or incomplete Camp workspace receipts. |
| `workspace.create` | `projectId`, `runId`, optional `baseCommit`; returns `workspaceId`, `sandboxId`, `containerName`, `mountPath`, `uid`, `gid`, and the frozen identity. |
| `workspace.checkpoint` | `workspaceId`, `message`, `quiescent: true`; returns the public checkpoint result, including `kind` and `head`. |
| `workspace.export` | `workspaceId`, new `targetArchive`, `quiescent: true`, optional environment `excludeNames`; returns `archivePath`, `bytes`, and `sha256`. Existing files are never overwritten. |
| `workspace.close` | `workspaceId`, optional `discard`; explicitly unmount and close the Run's Sandbox. |
| `workspace.list` | Camp's persisted creation and cleanup receipts, including incomplete operations. |
| `shutdown` | Close clients and acknowledge process shutdown; this does not imply Sandbox cleanup. |

Sources and export paths must resolve within the configured Camp data root. The trusted in-container exporter stops and reaps its tar child after 30 seconds or when its byte budget would be exceeded, before excess bytes reach SDK output storage. The default archive limit is 256 MiB; `maxArchiveBytes` accepts 1 KiB to 512 MiB. Environment exclusions are validated literal basenames. On failure, the partial host archive is removed and the Run owner stops and discards the Sandbox. Go still validates the successful archive before importing files.

The caller provides an immutable source and its identity: `baselineKey` must identify the actual frozen content, and `environmentKey` the prepared execution environment. Changing a directory behind an existing identity is unsupported.

A caller timeout after dispatch returns an unknown outcome. The Go client drains a late response before accepting another operation; it does not replay the first operation or confuse that response with the next request. A failed workspace creation returns its available cleanup receipt in error details.

## Restart and durability

LayerFS at this pin uses a Disposable Store and does not promise crash or power loss durability. Camp's Git resource objects and SQLite publication record remain authoritative. LayerFS checkpoints support the current execution, but are not a replacement for Camp's durable result store.

At native executor startup, Go calls `Bridge.Reset` before admitting work. Server never opens this bridge. Reset discards the previous private LayerFS projects and rebuilds future work from committed Camp resources. It does not open or attempt to repair a crashed Store. Cleanup finds Sandboxes through public runtime metadata even if a crash happened before Camp persisted the Run receipt. An unknown project/volume association or failed container stop makes execution unavailable; the application must surface that error instead of silently continuing. Uncommitted output after forced container termination is not recoverable through this contract.

## Checks and their scope

The keyless native probe imports binary and text files, creates isolated Sandboxes, writes and checkpoints a memo, exports exact bytes, reads public history, verifies orderly reopen, and removes its Store. It also checks that non-root commands cannot read Store files, daemon setup, or PID 1 credentials. The daemon TCP endpoint rejects unauthenticated plaintext and invalid Noise records; the probe separately inventories Unix sockets. These observations do not by themselves establish the command supervisor's network boundary.

The Go transport tests cover exclusive ownership, private configuration, environment filtering, cancellation, late responses, malformed protocol, and process reaping. Native Go tests cover independent Workspaces, archive identity, existing-output preservation, and startup discard after a bridge crash with a missing receipt and stopped daemon, while preserving another runtime home's active Sandbox. DSH tool and supervisor integration tests are separate gates.
