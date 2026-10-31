# Native Mac execution with a LayerFS Linux worker

Status: proposed implementation and acceptance contract for [#44](https://github.com/cofy-x/camp/issues/44). No production hybrid adapter or actual Mac-to-worker acceptance is claimed. Native-local remains the operational baseline while the comparison is performed; the final default is an outcome of that comparison.

## Scope and ownership

Agent commands and configured checks run natively on Mac ARM64 under Camp's existing Seatbelt policy and kernel-observed process owner. A separate Linux worker owns only LayerFS administration, workspace mirroring and checkpoint receipts. It is not a second Runner and receives no model credentials, enrollment authority, leases or Server publication authority.

Runner identity, storage/registration IDs, permissions, journal, cancellation, recovery and uploads retain their existing owners. Server Git/SQLite remains the durable publication authority. LayerFS's Disposable profile does not promise crash or power-loss durability; a worker acknowledgement must never be described as durable delivery.

The worker uses the [pinned LayerFS source](../tools/layerfs-runtime/pin.json) through its public API and a Linux-local Store shared by its SDK and daemons under one Linux kernel. Store data must not be placed on APFS, virtiofs, NFS or a Mac-shared directory. Qualify Linux ARM64 explicitly; Camp's existing Linux amd64 package is not ARM64 evidence. Build from a pinned private source archive, without modifying shared dependency checkouts.

The upstream macOS worker route is available to any integration. It forwards SDK calls; it does not expose a native Mac mount. Evaluate reusing that route against a checkpoint-only streamed transport. Preserve the upstream Store/concurrency semantics and public interfaces; do not introduce another Store encoder or database protocol.

## Workspace and checkpoint boundaries

The Mac directory is the sole active tool writer. Worker replicas are not writable by Agent commands. Do not synchronize live directories in the background or let SDK file tools and native commands modify independent copies concurrently.

Keep full working-tree checkpoints separate from accepted publication archives. Qualification must state whether it retains `.git`, ignored files, dependencies, executable and directory modes, symlinks and hardlinks. The [existing regular-file `fs.apply` proof](../tools/layerfs-runtime/hybrid/README.md) excludes these full-tree semantics and is not sufficient. The public API lacks a symlink-creation mutation; use supported workspace administration or another explicitly qualified representation. Unsupported entries must fail before mutation or remain clearly outside a restricted capability, never disappear silently.

Preserve opaque link metadata without following a link into host credentials, tool installations or another workspace. Checkpointing a link grants neither the worker nor a restored command permission to its target. Verify APFS case sensitivity, Unicode spelling/normalization and collisions with Linux names. Restoration must reject a namespace the destination cannot represent without renaming or overwriting files.

A checkpoint implementation belongs behind the workspace/checkpoint boundary, not in enrollment or Server storage. Publication capture and full-tree checkpoint capture may have different budgets and exclusions. Declare those separately; never apply the publication filter silently to a purported complete checkpoint. Exceeding a configured budget retains the source and reports failure.

## Sealing, receipts and recovery

Use the following ordering for each generation:

1. Freeze the attempt and source-generation identity. Close tool admission, drain admitted operations, finish required checks and establish descendant quiescence using the existing process owner.
2. Retain a sealed native snapshot and canonical manifest. Bind relative paths, entry types, modes, bytes/link metadata and content digests; validate again while streaming. A mutable source directory is not a sealed snapshot.
3. Persist a transfer intent before dispatch. Bind its operation ID to the Runner/storage namespace, workspace, attempt, generation, snapshot SHA-256, worker Store identity and expected parent checkpoint.
4. Transfer through an authenticated bounded channel. Validate lengths, paths, digests, quotas and ownership before installing a generation into an owned worker branch. One administrative writer controls that branch.
5. Commit through the public LayerFS API. Independently remount/read back the resulting checkpoint and compare its manifest, rather than trusting the sender's manifest or a returned head alone.
6. Return a receipt binding the exact generation, snapshot digest, parent and resulting checkpoint identity. Validate and retain it in Runner-owned recovery state before allowing an operation that requires that checkpoint to proceed. Server publication still uses the matching validated native result archive and its existing authority checks.

A lost response after dispatch is uncertain. Neither the client nor the worker blindly resends an attempted mutation or Commit. Read-only receipt/status recovery may confirm an already completed operation; conflicting bodies or changed parents are rejected. Otherwise retain native captures and uncertain receipts, block required checkpoint publication and require explicit recovery. Never substitute an older worker generation.

Test interrupted upload, apply failure, lost Commit response, worker/Runner restart and worker Store loss. Worker receipts and a Disposable Store do not replace retained native captures. Rebuilding a lost Store is an explicitly identified new operation from retained data, not a replay of an uncertain operation. Never reset an existing Runner or unrelated worker home to make a test pass.

## Transport, installation and permissions

Default worker control to a private local socket or authenticated loopback endpoint. Remote access requires an authenticated verified channel with an explicit endpoint, bounded framing and timeouts. Keep worker secrets outside command arguments, URLs, model tool environments, logs and report artifacts. Private configuration and receipts use owner-only permissions.

Commands must remain unable to read worker credentials, connect to its control endpoint, access Docker or reach other host/network resources. Apply the same denial checks to explicitly granted Mac toolchains and their descendants. An authenticated worker is trusted administration, not a general shell endpoint for Agents.

Pin the worker protocol, LayerFS source, native addon, daemon, image, architecture and runtime dependencies. Verify integrity and actual architecture before readiness. Do not silently select amd64 emulation or infer native ARM64 support. Record the selected Docker/OrbStack/Colima engine, its actual Linux kernel, socket, data directory, local Store filesystem and image identities.

Backend selection is an explicit stopped-Runner operation. Preserve identity, credential, storage/registration IDs, configuration fields, journals and existing user resources. A changed backend fingerprint may invalidate resume eligibility; it must not delete sessions or replay prompts. Keep old captures/receipts recoverable. Installation, foreground operation and optional service/login startup remain separate choices.

Report actual workspace, sandbox, checkpoint and worker provenance through existing diagnostics. An unopened or failed adapter must not report a fabricated ready worker. UI changes require direct inline before/after PR images.

## Actual Mac qualification

Use the operator's actual Apple Silicon Mac as the primary integration and measurement host, with its explicitly selected existing Linux environment. Record host and filesystem facts instead of assuming APFS or a particular VM product. Existing native-local acceptance may be reused at its recorded source/scope; add affected hybrid checks. Do not replace cross-OS observations with the Linux in-process proof or a simulated worker.

Use fresh test storage, owned projects/branches, synthetic files and a deterministic local provider. Never load the operator's enrolled Runner configuration, credentials, journal or private Server `.env`. Identity-upgrade tests use controlled fixtures and compare stopped configuration/identity/journal bytes before and after. Run no paid-provider acceptance without a separate finite allowance.

Correctness coverage must include:

- Native Mac execution using a narrowly authorized native toolchain, with the same read/write/network/descendant denial guarantees as native-local.
- Initial full capture; subsequent edit, truncate, binary replacement, executable/directory-mode change, rename, deletion, empty directories and an unchanged generation.
- Full working trees with dependency links and ignored files, plus explicit hardlink/special-entry handling; separately verify accepted publication exclusions.
- Case-sensitive/insensitive and Unicode behavior actually available on the host. Declare unsupported destination names and reject before destructive effects.
- Races with a writer, cancellation, incomplete descendant cleanup and changes during scan/stream; these must not produce a confirmed checkpoint.
- Truncated/corrupt/oversized transfers, wrong credentials, digest/generation/parent conflicts and path/link escape attempts.
- Lost replies and restarts at upload, apply and Commit boundaries, including a real completed Commit whose response is lost. Observe no mutation/model replay and no stale-generation publication.
- Repeated setup, backend changes, stopped backup/restore and restart preserve the controlled Runner identity/journal and both retained native captures and worker receipts.

Expose a hybrid capability as ready only after its applicable real self-test and identity/receipt compatibility checks succeed. A partial regular-file proof must remain labelled as partial.

## Cost comparison and default selection

Compare native-local, native plus the upstream worker route, and any proposed streamed checkpoint adapter on the same Mac, selected worker and exact workloads. Label a complete-Runner VM comparison only if it actually ran; it cannot establish Mac-native tool support.

Retain three individual measured repetitions per scenario: a small text tree, 10,000 small files and 128 MiB of deterministic incompressible data, followed by single-file edits, delete/rename and mode changes. Include a representative dependency/symlink tree. Separate cold installation/startup, first full snapshot, unchanged/warm checkpoint, incremental change and restore/fork. Do not call an already running Docker VM a cold VM start or restart the operator's existing services for a benchmark.

For every repetition record preparation/materialization, scan/hash, native capture, serialization, wire transfer, worker apply, Commit, readback verification and restore times. Record logical and actual wire bytes, files scanned/copied, Store/disk growth, client/worker/container CPU and sampled peak RSS; report VM costs separately when measurable. Avoid counting shared memory twice. The upstream base64/whole-frame transport cost must be measured, not inferred from changed file bytes.

Record source/binary/image/protocol/runtime identities, toolchain, host/VM facts, cold/warm definitions, reuse, sampling method and interference. Retain every pass/failure and individual sample, with median/range where useful; no best-of selection or artificial latency hidden outside the measured phase. Keep generated reports private and out of source, retaining sanitized evidence through CI/release policy.

Before measuring, record which capability and deployment cost would justify a default change: correct complete-tree recovery/fork/history, checkpoint/restore latency, memory/disk/transfer overhead, required privileges, installation/startup and failure recovery. Correctness and identity preservation are mandatory gates. Choose the default only after reviewing these observations; native execution alone and absence of Linux dependencies are distinct goals. Keeping native-local as default is valid if the hybrid's measured benefit does not justify its costs, while a qualified hybrid may remain an explicit option. Record limitations rather than declaring unmeasured parity or superiority.
