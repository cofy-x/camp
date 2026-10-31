# Native Mac execution with a LayerFS Linux worker

This contract owns the implemented optional Mac checkpoint adapter, complete-tree transport, retained generation branches and explicit Store reconstruction. Native-local is the recommended Mac default; the Linux checkpoint worker is an explicit capability choice. [Installation](install.md#optional-mac-layerfs-checkpoints) owns operator procedures, and each release's [evidence](releasing.md#release-evidence) records package availability and qualified environments.

## Default and deployment decision

Keep `native-local` as the Mac default. It executes native tools under Seatbelt without Linux administration, a second working-tree representation or a required cross-OS checkpoint. Its bounded publication archive serves ordinary Camp file work. It does not claim the worker's complete-tree history, immutable checkpoint forks or explicit Store reconstruction.

Choose `layerfs-worker` when those additional checkpoint capabilities justify a Linux engine, retained native captures, Linux-local Store storage and checkpoint latency. The actual-Mac comparison establishes those capabilities but also exposes substantial per-file capture, synchronization and restore costs. Large small-file trees remain expensive even when little content changes: the complete native generation must still be checked and sealed. Do not use the faster production publication-only baseline as evidence of complete-tree recovery parity. Dated samples, installation costs, collector failures and read-only rechecks belong in retained acceptance artifacts, not in a claimed universal performance guarantee.

Use the bounded streamed adapter for this optional backend. The upstream authenticated worker route remains a comparison and a public SDK integration reference. Its current `fs.apply` change set cannot create the complete dependency/link profile, and its authenticated whole-frame/base64 payload handling does not meet this transport's aggregate and chunk limits. A fast regular-file sample does not remove those capability and memory boundaries. Both routes continue to use the public LayerFS SDK; Camp does not implement another Store format.

Reuse an explicitly selected, qualified native ARM64 engine. An existing qualified engine needs no second VM. Exact engine/profile qualification belongs in release evidence; sharing the Docker API does not establish equivalent filesystem, isolation or performance results. Retained isolated Lima qualification does not imply a shipped Camp VM manager or a complete-Runner VM performance advantage.

The [provisioning boundary](#linux-environment-provisioning) and [current follow-up ownership](architecture.md#checkpoint-cost-and-retention) govern future VM/lifecycle and performance work.

## Scope and ownership

Agent commands and configured checks run natively on Mac ARM64 under Camp's existing Seatbelt policy and kernel-observed process owner. A separate Linux worker owns only LayerFS administration, workspace mirroring and checkpoint receipts. It is not a second Runner and receives no model credentials, enrollment authority, leases or Server publication authority.

Runner identity, storage/registration IDs, permissions, journal, cancellation, recovery and uploads retain their existing owners. Server Git/SQLite remains the durable publication authority. LayerFS's Disposable profile does not promise crash or power-loss durability; a worker acknowledgement must never be described as durable delivery.

The worker uses the [pinned LayerFS source](../tools/layerfs-runtime/pin.json) through its public API and a Linux-local Store shared by its SDK and daemons under one Linux kernel. Store data must not be placed on APFS, virtiofs, NFS or a Mac-shared directory. Qualify Linux ARM64 explicitly; Camp's existing Linux amd64 package is not ARM64 evidence. Build from a pinned private source archive, without modifying shared dependency checkouts.

The upstream macOS worker route is available to any integration. It forwards SDK calls; it does not expose a native Mac mount. Evaluate reusing that route against a checkpoint-only streamed transport. Preserve the upstream Store/concurrency semantics and public interfaces; do not introduce another Store encoder or database protocol.

Architecture qualification includes the base image's resolved manifest/config, not just its tag. The existing Camp Linux runtime installer and pinned base-image digest target amd64; they are not an ARM64 build input by implication. Give the ARM64 worker its own immutable base-image, addon, daemon and runtime identities. Build from an isolated source archive, verify ELF architecture and actual native execution, then qualify Store open, FUSE mount, Commit and remount before claiming a usable worker. A failed build or unsupported public operation is a retained qualification outcome, not permission to patch the shared checkout or use emulation.

## Workspace and checkpoint boundaries

The native Mac directory is the sole active tool writer. Worker replicas are checkpoint administration, not another command workspace. Never synchronize live directories or let SDK tools and native commands write independent copies.

The complete-tree manifest binds exact relative names, types, Unix file/directory modes, content digests, opaque symlink targets and internal hardlink groups, including `.git` and ignored dependencies. Preserve hardlink topology on round-trip. External hardlink groups, sockets/FIFOs/devices, special permission bits, non-UTF-8 names and names containing backslashes fail explicitly; owners, ACLs, xattrs, timestamps, sparse allocation and platform flags are outside this profile. Capturing a link never grants access to its target.

[Publication archives](../apps/server/internal/workspace/workspace.go) have separate filters, type rules and budgets; applying them to a complete checkpoint would lose data. Budget failures retain the source. The [regular-file `fs.apply` proof](../tools/layerfs-runtime/hybrid/README.md) lacks this full-tree profile; the upstream mutation API cannot create its symlinks, so another qualified public workspace operation is required.

Restore into a new private staging directory on the destination filesystem. Reserve exact case/Unicode names and detect collisions before materializing files/links, then independently compare the complete manifest. Never normalize, rename, merge or overwrite to hide a collision. Failure preserves existing work and the retained capture. Qualify both ordinary and case-sensitive APFS when available using representative dependency/link fixtures.

## Sealing, receipts and recovery

Use the following ordering for each generation:

1. Freeze the attempt and source-generation identity. Close tool admission, drain admitted operations, finish required checks and establish descendant quiescence using the existing process owner.
2. Retain a sealed native snapshot and canonical manifest. Bind relative paths, entry types, modes, bytes/link metadata and content digests; validate again while streaming. A mutable source directory is not a sealed snapshot.
3. Persist a transfer intent before dispatch. Bind its operation ID to the Runner/storage namespace, workspace, attempt, generation, snapshot SHA-256, worker Store identity and expected parent checkpoint.
4. Transfer through an authenticated bounded channel. Validate lengths, paths, digests, quotas and ownership before installing a generation into an owned worker branch. One administrative writer controls that branch.
5. Commit through the public LayerFS API. Independently remount/read back the resulting checkpoint and compare its manifest, rather than trusting the sender's manifest or a returned head alone.
6. Return a receipt binding the exact generation, snapshot digest, parent and resulting checkpoint identity. Validate and retain it in Runner-owned recovery state before allowing an operation that requires that checkpoint to proceed. Server publication still uses the matching validated native result archive and its existing authority checks.

Persist distinguishable preparation, upload completion, mutation/Commit dispatch, readback and confirmation states; dispatch is recorded before the mutating call. Lost replies remain uncertain, even if the worker continues. Never blindly replay a mutation/model request or substitute an older generation.

Reconciliation observes public history/readback with the matching operation, generation, digest, parent and Store identity; conflicting bodies/parents fail. A local receipt alone cannot establish current state after restart/Store loss. An unchanged snapshot can advance its generation receipt with the same head, without inventing a Commit. Otherwise retain captures/receipts and block required checkpoint publication until explicit recovery. Reconstruction uses a new Store/operation, preserving old uncertainty and lineage.

Qualification covers interrupted upload, apply failure, lost Commit reply, worker/Runner restart and Store loss without resetting another Runner or home. [Actual Mac qualification](#actual-mac-qualification) owns the remaining fault/fixture matrix.

## Transport, installation and permissions

The shipped local-engine transport is an inherited private pipe through the selected Docker socket, with no published control port. Runner owns both ends, verifies image/container identity, filters environment and serializes bounded requests with backpressure/timeouts. EOF closes admission and drains dispatched work; a lost pipe reconciles by observation even if Docker/worker continued. Any future remote transport needs an explicit authenticated, verified endpoint with equivalent bounds.

Docker/worker credentials, endpoints and descriptors must stay inaccessible to commands and approved-tool descendants, and outside arguments, URLs, model environments and logs. Configuration/receipts are owner-only. Verify pinned protocol, LayerFS/addon/daemon/image/runtime identities and actual ARM64 architecture; no emulation fallback. Record the selected engine's kernel, socket, data directory and Linux-local filesystem.

[Stopped backend selection](install.md#optional-mac-layerfs-checkpoints) preserves Runner identity/journal/data. A changed runtime/Store fingerprint can invalidate session resume without deleting it. Package the matching controller/pin in Mac runtime and the checksummed image separately; native-local never loads an unused image. Persist the explicit Docker executable for foreground/launchd use without interactive PATH assumptions. Setup, worker/VM lifecycle and login startup remain separate.

Selecting the hybrid requires a confirmed checkpoint before completion. Failure retains native work/receipts and suspends the backend; it never silently falls back or grants worker lease/publication authority.

The hybrid's publication view is derived from its retained, confirmed complete-tree capture, rather than rereading the mutable native directory. Exclude `.git` and the declared environment publication exclusions, then materialize accepted hardlink aliases as distinct regular files with the same frozen bytes. Preserve link topology and all excluded membership in the complete checkpoint. A remaining symlink or another entry unsupported by the Server archive is an explicit publication refusal; do not silently remove a requested result or weaken the Server's path, credential-name, type and byte checks. A failed publication keeps the complete capture and native recovery directory.

Keep an operation-owned Branch for each checkpoint generation. Fork it through the public SDK from the last confirmed Branch, record dispatch before the fork, and never mutate that Branch again after confirmation. This retains an exact public reference for independent historical restore and fork without depending on private Store records or changing an existing Branch head back in place. An unchanged generation may reference the same Commit through another operation-owned Branch. Native publication also checks the current workspace generation and operation receipt; an older verified checkpoint remains restorable, but cannot become the current attempt's publication view.

Explicit reconstruction after Store loss creates a new owned Store epoch and a new operation from a retained native capture. Bind the old Store/operation/generation, known or uncertain checkpoint outcome, parent and snapshot digest as recovery provenance; do not reuse the old mutation ID or replay a model request. Select the new epoch only after its independent checkpoint readback succeeds. Retain the old epoch, captures, pending receipts and reconstruction intent if any step is interrupted. The changed Store fingerprint can invalidate session resume without deleting identity, journals or model-session data. Native-only recovery remains an explicit restoration of retained bytes, labelled separately from a current verified LayerFS checkpoint and Server publication.

Retained capture references are relative to the owning worker home, including references across Store epochs. Resolve them under the current private home after a stopped backup is copied to a new location; reject absolute or escaping relative references. Do not guess a new location for an older absolute-only receipt that no longer belongs to the current home. Preserve it for explicit inspection. Relocation does not change Runner/Store identity or replay a prompt, although its changed native runtime path can invalidate session resume eligibility.

### Upstream lifecycle and transport adaptation

Reuse the upstream SDK and worker route without adopting their launcher defaults as Camp policy. At the pinned source, the macOS launcher uses `--restart unless-stopped`, starts a stopped worker, replaces a differently built container, forwards the launcher's environment to Docker, and can start during project Init. Runtime open also enables stopped-Sandbox sweeping by default. Camp must own explicit startup, shutdown, replacement and scoped recovery, use private names/homes/TMP directories and a filtered environment, and never sweep another owner's resources. Qualification may launch the unmodified worker entry point under a Camp-owned supervisor; it must not modify upstream source to change those defaults.

The upstream wire format authenticates a bounded first frame, then buffers complete frames and represents binary values as base64. Authenticated payloads have no equivalent configured frame bound. Treat that behavior as a measured comparison arm in private fixtures, not as an already bounded production transport. A production adapter needs explicit aggregate/per-entry limits, bounded chunks and in-flight work, backpressure, timeouts and pre-mutation validation. Keep credentials in private configuration and out of command arguments, URLs, Agent environments and reports. A checkpoint-only adapter may call public LayerFS operations inside Linux; it must not add a Store encoder or expose a general command endpoint to Agents.

Report actual workspace, sandbox, checkpoint and worker provenance through existing diagnostics. An unopened or failed adapter must not report a fabricated ready worker. UI changes require direct inline before/after PR images.

### Linux environment provisioning

The shipped adapter consumes a selected engine, not a Camp-managed VM. Isolated Lima qualification and cold/warm/resource samples apply only to their retained profile; commands still run natively on Mac. Select endpoints per owned process without changing the global Docker context.

A future provisioner must preserve existing-engine support and satisfy these boundaries:

- Use the public Lima CLI and pinned guest/provisioning/worker inputs; native ARM64 VZ with emulation disabled. Upstream [Docker profiles](https://lima-vm.io/docs/examples/containers/docker/) and [VZ](https://lima-vm.io/docs/config/vmtype/vz/) describe components, not Camp acceptance.
- Use rootful guest Docker and Linux-local Store volumes. Default containerd/rootless templates are not qualified substitutes.
- Own a private home/SSH identity; import no host keys, forward no agent and mount no host home. Limit administrative sockets/ports and retain TLS/SSH host verification.
- Explicitly control environment and system-proxy inheritance, verifying the guest's effective values; filtering only the launching environment is insufficient.
- Keep create/start/stop, upgrade/replacement and login startup explicit. Preserve Runner identity and unrelated instances. Qualify lifecycle/recovery and installation/resource costs independently of transport.

## Actual Mac qualification

Use the operator's actual Apple Silicon Mac as the primary integration and measurement host, with an explicitly selected Linux environment. Qualify an existing engine or an owned isolated guest at its actual profile; do not restart unrelated services to manufacture a cold-start sample. Record host and filesystem facts instead of assuming APFS or a particular VM product. Existing native-local acceptance may be reused at its recorded source/scope; add affected hybrid checks. Do not replace cross-OS observations with the Linux in-process proof or a simulated worker.

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

Compare native-local, native plus the upstream worker route, and Camp's bounded streamed checkpoint adapter on the same Mac, selected worker and exact workloads. Label a complete-Runner VM comparison only if it actually ran; it cannot establish Mac-native tool support.

Retain three individual measured repetitions per scenario: a small text tree, 10,000 small files and 128 MiB of deterministic incompressible data, followed by single-file edits, delete/rename and mode changes. Include a representative dependency/symlink tree. Separate cold installation/startup, first full snapshot, unchanged/warm checkpoint, incremental change and restore/fork. Do not call an already running Docker VM a cold VM start or restart the operator's existing services for a benchmark.

For every repetition record preparation/materialization, scan/hash, native capture, serialization, wire transfer, worker apply, Commit, readback verification and restore times. Record logical and actual wire bytes, files scanned/copied, Store/disk growth, client/worker/container CPU and sampled peak RSS; report VM costs separately when measurable. Avoid counting shared memory twice. The upstream base64/whole-frame transport cost must be measured, not inferred from changed file bytes.

Use the same sealed input manifests for comparable arms and account for each arm's required preparation. Native-local publication capture is not a full-tree restore baseline. If an upstream route cannot represent a dependency/link fixture, retain that case as unsupported rather than removing entries or calling it a successful full-tree comparison. Keep capability coverage distinct from timings; any common full-tree local capture/restore added for the comparison needs its own affected native qualification. Reuse existing acceptance only when source/runtime identity and unaffected scope are verifiable.

Record source/binary/image/protocol/runtime identities, toolchain, host/VM facts, cold/warm definitions, reuse, sampling method and interference. Retain every pass/failure and individual sample, with median/range where useful; no best-of selection or artificial latency hidden outside the measured phase. Keep generated reports private and out of source, retaining sanitized evidence through CI/release policy.

Complete-tree capture may read, hash and synchronize ordinary file blobs concurrently within a fixed small budget. Enumerate and retain metadata witnesses first, validate every file before and after reading, await all admitted content work even on failure, then repeat namespace and metadata checks before sealing. Keep hardlink canonicalization deterministic and bound open descriptors and buffers. Parallel phase sums can exceed wall time and must be labelled as such. Retain superseded sequential measurements and interrupted campaigns when improving this cost; only comparable completed repetitions of the selected implementation establish its final comparison.

Mac captures retain content of at most 64 KiB per unique digest in a capture-owned small-file pack. Large content keeps the individual-blob representation. An indexed range is bounded by the existing capture limits and is independently hashed against the unchanged complete-tree manifest whenever read. Synchronize the pack and its validated relative index before making the sealed receipt visible. A missing, malformed, truncated or conflicting pack/index fails closed; no incomplete pack is reinterpreted as a legacy capture. Old individual-blob captures remain readable, and both representations use the same bounded raw transport and LayerFS public operations. This format is private local retention, not a new LayerFS Store format or a reuse of an unchecked generation.

Fresh Mac exports can receive into the same packed representation; the shared Linux blob cache retains its existing individual files. Restore reserves all exact names before materializing content, uses bounded file work and retains per-file synchronization plus independent full-manifest readback. All original quiescence, link, collision, uncertainty and relocation requirements remain applicable. Report small-file pack seal time separately from scan/hash and include both in total capture cost. Every generation still enumerates and hashes the full source; no metadata-only cache bypass is introduced.

Before proposing a default change, record its required capability and deployment-cost evidence: correct complete-tree recovery/fork/history, checkpoint/restore latency, memory/disk/transfer overhead, required privileges, installation/startup and failure recovery. Correctness and identity preservation are mandatory gates. Change the default only after reviewing these observations; native execution alone and absence of Linux dependencies are distinct goals. Keeping native-local as default is valid if the hybrid's measured benefit does not justify its costs, while a qualified hybrid may remain an explicit option. Record limitations rather than declaring unmeasured parity or superiority.
