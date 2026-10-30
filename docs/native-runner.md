# Native Runner execution and workspace design

Status: active design and acceptance contract. The released implementation is still the Linux Runner described in [execution](dsh.md). A passing source check does not establish macOS isolation or packaged Runner readiness.

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

## Workspace alternatives

| Combination | Mac-native tools | Linux dependency | Cost and consistency |
| --- | --- | --- | --- |
| Native directory + Seatbelt + local checkpoint | Yes | No | One owned local tree; seal writers before bounded capture; no cross-OS mirror |
| Native directory + Seatbelt + LayerFS Linux worker | Yes | Yes | Native tree plus worker mirror; explicit transfer and generation receipt required |
| Complete Runner in Linux VM + LayerFS | No | Yes | Existing Linux semantics inside a managed guest; VM resources and Linux toolchains |

Native local execution is the first qualification target. Workspace selection is independent of the sandbox selection: adopting Seatbelt does not remove LayerFS. Keep the Linux implementation and investigate the hybrid against the same input, capture and cancellation contract before deciding its Mac default.

LayerFS's worker forwards SDK calls; its Linux FUSE mount does not become a Mac working directory. A Mac process needs a local materialization or a separately implemented filesystem transport. Do not put the Store on virtiofs/APFS or claim zero-copy native access. Linux Store openers must retain the upstream shared kernel and local filesystem requirements.

For the hybrid proof, the Mac tree is the sole active writer. Start from a frozen generation; seal tool admission, finish configured checks and confirm all descendants before capture. Transfer one complete bounded snapshot into an owned worker Workspace through trusted administrative operations, then Commit and record the native archive digest, worker Commit and generation. Never synchronize in the background while tools write. Apply failures or a lost Commit reply retain the native capture and an uncertain checkpoint receipt; do not resend the mutation or report the worker's older tree as current. Only an acknowledged matching generation can supply a required checkpoint.

Compare a complete snapshot with incremental synchronization. An incremental route must represent deletes, renames, executable modes, binary files and excluded dependency trees, not just copy changed file bytes. APFS case folding, Unicode normalization and symlink behavior must be checked explicitly. A fresh Store per checkpoint loses shared history/deduplication and is not an equivalent implementation. Mounting Linux FUSE back into macOS introduces another runtime and permission boundary and is not assumed to be lightweight.

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
