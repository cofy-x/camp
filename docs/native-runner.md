# Native Runner execution and workspace design

Status: design and acceptance contract. Source implements Linux LayerFS and Mac ARM64 native local execution. Mac package distribution has its own release acceptance; a passing source check does not establish installation readiness.

## Ownership and platform goals

Runner owns enrollment, identity, authorization leases, frozen inputs, command deduplication, cancellation, recovery, and retained upload receipts. Server owns permissions, model credentials, shared publication, and human delivery. These owners do not depend on Seatbelt, Docker, LayerFS, or a particular workspace.

There are two separate goals: executing authorized **Mac-native tools**, and requiring **no Linux environment**. Native execution with a Linux checkpoint worker can satisfy the first without satisfying the second. A complete Linux VM does not execute Mac-native tool binaries. It remains a Linux execution option, not the only Mac architecture or an automatic fallback.

Separate the execution adapter into four responsibilities:

- Workspace: prepare an isolated mutable directory from frozen input; expose references separately; seal, capture and dispose only owned files.
- Sandbox: turn explicit read/write/tool/network authority into an OS boundary for one process tree, without taking enrollment or publication authority.
- Process owner: start, stream, cancel and establish descendant quiescence. A process exit or closed output pipe is not a cleanup receipt.
- Checkpoint: retain a sealed workspace generation and return its content identity. A checkpoint is not Server publication or crash-durable delivery.

Keep the existing capability-specific filesystem and command tools. Read-only Agents still receive no write or shell capability. Every adapter must return the same bounded archive and cleanup facts to the existing Runner journal and Server publication path. The existing Linux LayerFS adapter remains supported. Changing an adapter must preserve Runner/storage/registration IDs, credentials, journal, resources and sessions; an incompatible session can become explicitly non-resumable without deleting the Runner identity or replaying a prompt.

## Seatbelt qualification

Reference: sandbox-runtime `98a4016d27609ed34dd1fb43d23ed6efb6eeeb77` (`@anthropic-ai/sandbox-runtime` 0.0.81). Read shared source only; pin any shipped dependency inside Camp. Its default read policy allows reads everywhere. Its macOS `cleanupAfterCommand()` is a no-op, and its CLI signals its direct child. Neither is Camp's required permission or descendant-cleanup guarantee.

Use a Camp-owned policy with default-denied file data reads and writes. Allow only the current workspace, exact references, private per-command HOME/TMP, necessary system runtime files and explicitly approved native tool roots. Resolve literal paths and symlink targets; never widen a permission because a path contains glob syntax. Never grant all of `/System` (which includes the Data volume), `/Users`, `/Volumes`, `/private`, a user home or Homebrew merely to make a command pass. Directory traversal metadata is distinct from reading directory contents or file data. Other Runs, Runner config/journal, Server data, `.env`, SSH/cloud credentials, Keychain and host sockets stay inaccessible.

Commands and configured checks have no network by default, including direct IPv4/IPv6, UDP/DNS, loopback and Unix sockets. An empty domain list alone is not sufficient proof. Disable local binding, arbitrary Unix sockets, Apple Events, weaker network isolation and discretionary permission callbacks. DSH's model/tool control channels are a separate private profile: permit only its attempt-scoped proxy and capability endpoints, never expose those capabilities to commands. An approved executable does not implicitly authorize its user's credentials, launch agents, GUI automation, signing service or network access.

Camp must qualify a process owner that handles background children, changed process groups, double forks, `setsid`, ignored signals, closed stdio and parent death. Use kernel-observed process identities; do not signal a reused PID, another Run or an unrelated user process. A process-group kill alone is insufficient. If termination or ownership cannot be established, mark cleanup unknown, block capture/publication and suspend execution. Do not silently weaken the policy, enable unsandboxed execution or switch to a VM.

## Dependency preparation boundary

Prepared Node dependencies are a separate trusted operation, with a frozen lockfile and lifecycle scripts/pnpm hooks disabled. On Mac, qualify it through the same kernel-owned process lifecycle as native commands, with a writable staging project/store, private HOME/TMP and no ambient credentials. The package manager must not receive general networking: an authenticated, per-install loopback CONNECT broker can reach public HTTPS endpoints on port 443, validating every resolved address and dialing the checked address directly. Private/loopback/link-local/reserved destinations, other ports/methods, unauthenticated clients, local binding and direct DNS/IP/Unix networking remain denied. The tunnel preserves end-to-end TLS verification rather than intercepting package TLS.

Before exposing this path, prove a locked pnpm install, disabled install scripts, cache reuse, host credential/config denial, public package transfer and private-destination rejection on actual Mac ARM64. Confirm all owned descendants before publishing a ready cache marker or deleting staging files. Unknown cleanup retains staging/receipts, suspends execution and is recovered without replay at Runner startup. Model commands and configured checks continue to receive no package-network capability. Linux's existing dependency path remains independently qualified.

## Operator setup and lifecycle

Mac setup runs as the original unprivileged account and recognizes interactive terminals, hiding the connection code and restoring terminal echo. Repeated setup uses the saved registration and private journal. Source builds use `--development-runtime`; complete native packages have a separate distribution acceptance.

During stopped setup, repeat `--tool-root /absolute/tool/directory` to replace the saved native tool grants, or use `--clear-tool-roots` to revoke them. Omitting both retains previous grants. Grants resolve aliases to existing directories and permit read/execute only. Broad host roots, home ancestors, Runner storage and SSH/cloud/Keychain credential trees are refused. Prefer a specific tool installation, such as a resolved Homebrew Cellar version or Xcode toolchain; a tool upgrade that changes its canonical path requires an explicit grant update. These grants affect command PATH and sandbox authority, without inheriting the operator's environment or authorizing signing/network services.

The Mac `runner service` commands manage a private launchd job under the same account. `install` requires the complete package and writes its configuration without starting a process or enabling login startup. `start` runs independently of the terminal; `stop` requests graceful shutdown. `enable-login` and `disable-login` separately manage the corresponding owned LaunchAgent file. `remove` stops the owned job and removes service files while retaining registration, journal, workspaces and resources. Changing tool grants or updating service installation requires the Runner to be stopped. The daemon reconnects itself; a process that exits remains stopped until explicitly started, preventing initialization replay after an error requiring attention. Plain source runtimes support foreground execution.

## Workspace alternatives

| Combination | Mac-native tools | Linux dependency | Cost and consistency |
| --- | --- | --- | --- |
| Native directory + Seatbelt + local checkpoint | Yes | No | One owned local tree; seal writers before bounded capture; no cross-OS mirror |
| Native directory + Seatbelt + LayerFS Linux worker | Yes | Yes | Native tree plus worker mirror; explicit transfer and generation receipt required |
| Complete Runner in Linux VM + LayerFS | No | Yes | Existing Linux semantics inside a managed guest; VM resources and Linux toolchains |

Native local execution is the first qualification target. Workspace selection is independent of the sandbox selection: adopting Seatbelt does not remove LayerFS. Keep the Linux implementation and investigate the hybrid against the same input, capture and cancellation contract before deciding its Mac default.

The Mac local provider is the default implementation: no Docker, Linux VM, FUSE or root account is required. Its native command owner, protected file operations, DSH/TLS relay, binary/mode capture, continuation, cancellation, collaboration decisions and failure boundaries are exercised on actual macOS 15 and 26 ARM64 CI. Retained measurements separately cover local preparation/capture and the Linux Store proof; hosts and transport scopes must remain explicit. Stable per-session directory names solve DSH's persisted working-directory contract without broadening reads to earlier attempts. Each request still starts from frozen input, and old interrupted copies are moved to private recovery storage only after cleanup is established.

LayerFS's worker forwards SDK calls; its Linux FUSE mount does not become a Mac working directory. A Mac process needs a local materialization or a separately implemented filesystem transport. Do not put the Store on virtiofs/APFS or claim zero-copy native access. Linux Store openers must retain the upstream shared kernel and local filesystem requirements.

For the hybrid proof, the Mac tree is the sole active writer. Start from a frozen generation; seal tool admission, finish configured checks and confirm all descendants before capture. Transfer one complete bounded snapshot into an owned worker Workspace through trusted administrative operations, then Commit and record the native archive digest, worker Commit and generation. Never synchronize in the background while tools write. Apply failures or a lost Commit reply retain the native capture and an uncertain checkpoint receipt; do not resend the mutation or report the worker's older tree as current. Only an acknowledged matching generation can supply a required checkpoint.

Compare a complete snapshot with incremental synchronization. An incremental route must represent deletes, renames, executable modes, binary files and excluded dependency trees, not just copy changed file bytes. APFS case folding, Unicode normalization and symlink behavior must be checked explicitly. A fresh Store per checkpoint loses shared history/deduplication and is not an equivalent implementation. Mounting Linux FUSE back into macOS introduces another runtime and permission boundary and is not assumed to be lightweight.

The pinned public `fs.apply` can publish a batch of regular-file writes, deletes, directory changes and chmods once, without making the native working directory a FUSE mount. It has no create-symlink change, so a mirror of Camp's accepted publication files is distinct from a complete working-tree checkpoint with dependencies and links. The official worker encodes binary buffers as base64 in length-prefixed JSON and buffers complete authenticated frames; logical changed bytes therefore understate wire and memory costs. A Camp checkpoint-only Linux worker with streamed capture input is a possible separate adapter, not a complete Runner VM. It must preserve the same Store, single-writer generation receipts and uncertainty rules. Mac transport and Linux ARM64 worker qualification remain separate from an in-process Linux Store proof.

## Evidence and acceptance

Run the following without model keys. Record exact source/runtime identities, actual platform, outcomes and skips; keep receipts in CI/release artifacts.

| Area | Required observations |
| --- | --- |
| Read boundary | Workspace/reference/tool reads succeed; unrelated home, sibling Run, hidden credentials, canonical aliases, outside symlinks and path names containing glob characters are denied |
| Write boundary | Owned workspace/HOME/TMP writes succeed; references, approved tool installations, parent directories and Runner/Server state stay unchanged |
| Network | Direct IP, IPv6, DNS/UDP, localhost listeners, Unix sockets and inherited proxy environment cannot bypass the command policy; only exact DSH control endpoints work |
| Descendants | Normal exit, cancellation, timeout, ignored TERM, background/double-fork/setsid children, closed pipes and parent death leave no owned writer; unrelated and concurrent Runs survive |
| Capture | Binary bytes, executable mode, deletes/renames, concurrent writers, cancellation during capture and unknown cleanup cannot produce a falsely successful result |
| Hybrid | Local archive and remounted LayerFS checkpoint have identical accepted files/modes; interrupted transfer, lost Commit reply and restart preserve uncertainty and never reuse a stale generation |
| Identity | Repeated setup, explicit upgrades, damaged caches and backend changes preserve enrollment and journal; incompatible resume is rejected without replay |
| Native tools | Approved system tool and an explicitly approved external toolchain run on Darwin arm64; denied reads/network stay denied for their children |

Measure local-only and hybrid paths on the same deterministic workloads: a small text task, 10,000 small files, a 128 MiB binary tree, and subsequent single-file edit/delete/rename/mode changes. Report cold setup, warm start, capture/transfer/Commit time, files scanned, bytes transferred and RSS separately. Run three measured iterations after setup; retain every result. Linux worker measurements establish transfer/Store behavior, not APFS or Mac performance. Include complete-VM measurements only when that environment actually ran.

macOS CI can qualify native execution without nested virtualization. The user will additionally run the retained real-machine acceptance tool against their authorized toolchain. Do not label a Linux source test, simulated process owner or mocked worker as Mac native/hybrid acceptance. Expose a backend as ready only after its actual permission, process and workspace self-test passes.

Implementation order: record this contract and comparison; qualify Seatbelt and the process owner; extract backend-independent tools/lifecycle; implement native workspaces and the hybrid proof; select defaults from those results; then update packaging, diagnostics, onboarding and immutable release verification. UI changes need before/after evidence. Review and squash merge each PR after its applicable checks pass; keep unqualified paths explicit rather than claiming completion.
