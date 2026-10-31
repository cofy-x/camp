# Camp tool providers

These modules implement the public `FileSystem` and `ShellExecutor` seams from DSH **0.2.1-rc.3**. Go's `dsh.RemoteOverlay` owns the production `sdk-minimal` overlay; `patch.mjs` is the equivalent isolated JavaScript probe fixture. There are no shared-checkout imports and no direct filesystem or subprocess calls. DSH still owns its private session persistence and attachment storage.

The write profile exposes `read`, `write`, `edit`, `list_files`, `search_files`, and foreground `bash`. The read profile exposes only the first read operation and the two discovery tools. Both disable local filesystem, subprocess, PTY, persistent terminals, background jobs, working-directory tools and image reading. The working-directory service uses Camp's filesystem provider. The bash tool is the published DSH tool with background execution and timeout promotion disabled. The public filesystem observation policy forwards the version observed by DSH's read/write tools into guarded mutations; an old observation cannot silently overwrite a newer file version.

`CAMP_TOOL_ENDPOINT` is a loopback HTTP origin; `CAMP_TOOL_TOKEN` is a capability scoped to one Run. Both belong only to the DSH process environment. Requests use `Authorization: Bearer …`, disallow redirects and never expose these values in tool schemas, arguments, model context or diagnostic messages. Camp's capability service validates the Run, file roots and read/write access on every operation. Linux displays `/workspace` and `/references`; Mac returns the authorized native paths while accepting those logical aliases for file operations. The plugin performs no host-path mapping, and arbitrary shell text is never rewritten. The current discovery defaults still advertise `/workspace`; [native path guidance](../../../docs/architecture.md#native-path-guidance) tracks that mismatch.

## File protocol

`POST /fs` accepts the following JSON requests. Every response is either `{"value": …}` or `{"error":{"code":"FS_…","message":"…"}}`.

| Operation | Request fields besides `operation` | Value |
| --- | --- | --- |
| `resolve` | `path`, optional `cwd` | `{targetKey,displayPath}` with the backend's authorized display path |
| `stat` | `target` | `null` or `{version,type,size?}` |
| `lstat` | `path`, optional `cwd` | `null` or `{version,type,size?}`, including `symlink` |
| `readText` | `target` | Complete validated UTF-8 string |
| `readBytes` | `target,maxBytes` | Complete base64 content within the inclusive limit |
| `readByteRange` | `target,offset,length` | Base64 content for the bounded byte window |
| `listDir` | `target` | `[{name,type,target,version?,size?}]` in stable order |
| `writeText` | `target,content,expected?` | `{operation:'create'\|'update',version,before,after}` |
| `editText` | `target,edit:{oldString,newString,replaceAll},expected?` | `{version,before,after}` |

`type` is `file`, `directory` or `other`, plus `symlink` for `lstat`. A write's `expected` is `{kind:'createIfAbsent'}` or `{kind:'replaceIfVersion',version}`. An edit's `expected` is `{version}`. Go must check the guard and perform matching and atomic mutation in the same critical section. `before` can be null on writes; diff basis text is LF-normalized, while stored bytes preserve the provider's documented line-ending policy. `streamText` uses one bounded complete read; it does not promise unbounded file streaming.

The public error vocabulary is `FS_NOT_FOUND`, `FS_NOT_DIRECTORY`, `FS_NOT_TEXT`, `FS_NOT_REGULAR_FILE`, `FS_TOO_LARGE`, `FS_PERMISSION_DENIED`, `FS_SANDBOX_DENIED`, `FS_IO_ERROR`, `FS_STALE_VERSION`, `FS_NOT_OBSERVED`, `FS_AMBIGUOUS_EDIT`, `FS_EDIT_NOT_FOUND` and `FS_ABORTED`.

Discovery traverses this provider, never local ripgrep. It visits at most 2,000 entries. Literal search reads regular UTF-8 files of at most 256 KiB, with an 8 MiB aggregate read budget, 100 matches and 32 KiB output. Oversized/binary files are explicitly reported as skipped; bounded results say to narrow the search.

## Command protocol

`POST /shell` takes `{commandId,command,workdir,timeoutMs,stdoutMaxBytes}`. The client generates a UUID before submission. Each command runs independently; there is no persistent shell, background job, arbitrary environment or stdin override. DSH's managed environment is deliberately not forwarded into commands. Defaults are 30 seconds and 64 KiB captured stdout, capped at 120 seconds and 64 KiB; the Go server remains authoritative for actual limits.

The response is UTF-8 NDJSON:

```json
{"type":"stdout","text":"actual output"}
{"type":"stderr","text":"actual error output"}
{"type":"result","cleanupConfirmed":true,"result":{"exitCode":0,"signal":null,"timedOut":false,"aborted":false,"timeoutMs":30000,"stdout":{"text":"actual output","truncated":false},"stderr":{"text":"actual error output","truncated":false}}}
```

The terminal result contains bounded authoritative output tails. Only one of `timedOut` and `aborted` may be true. A successful process exit without confirmed cleanup is not a successful tool result. A missing/invalid terminal response remains an infrastructure failure even when partial output exists.

`POST /shell/cancel` takes `{commandId}` and returns `{"value":{"cleanupConfirmed":true}}` only after the command's process tree is reaped. It must handle cancellation arriving before command admission using a tombstone. The plugin keeps reading the original stream while cancellation is acknowledged. Disconnects trigger the same cleanup request; unconfirmed cleanup is an error, and Go must revoke the Run and reject further tool work. Shutdown cancels and awaits every owned command. Go owns final container cleanup and Run result eligibility even if the DSH process is killed.

## Verification

```sh
node --test tools/dsh-runtime/camp-tools/camp-tools.test.mjs
node tools/dsh-runtime/camp-tools-probe.mjs
CAMP_TEST_LAYERFS_RUNTIME="$PWD/tools/layerfs-runtime" \
  CAMP_TEST_DSH_RUNTIME="$PWD/tools/dsh-runtime" \
  go test -race ./apps/server/internal/layerfs -run TestLayerFSDSHIntegration -count=1 -v
```

The probe launches the actual pinned DSH under Bubblewrap against keyless model and bridge fixtures. It verifies exact tool catalogs, real tool dispatch through both public providers, forbidden paths, absent read-only mutation/command tools, and clean shutdown. It does not test Docker, LayerFS, real shell execution or model quality; the server integration tests establish those separate guarantees. The native Go test uses the production overlay and real LayerFS container. It covers command/file dispatch, non-root execution, private configuration absence, reference reading, checkpoint/export, original preservation and detached-process cleanup on both normal completion and explicit cancellation.

## Collaboration tools

`coordination.mjs` is installed only for an explicit Camp collaboration round. It registers `camp_handoff`, `camp_ask_person`, and `camp_finish` through the public DSH tool API. The separate `CAMP_COORDINATION_ENDPOINT` and `CAMP_COORDINATION_TOKEN` remain in the private DSH environment. One `POST /intent` submits the public tool call ID and typed decision to a loopback coordinator. The coordinator validates participants and relative file paths, seals/drains workspace tools, and persists the intent before returning `{value:{accepted:true}}`.

Only that acknowledgement calls `exec.concludeTurn()`. A pre-step listener consumes queued context without another model call after conclusion. Tools never dispatch another Agent directly; Camp Server owns draft checks, cleanup acceptance, task state and scheduling. The intent and complete result have separate durability: a persisted intent alone is not enough to resume or publish after a crash.
