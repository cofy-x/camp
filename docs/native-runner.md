# Native Runner execution and workspace design

This contract owns the implemented Linux LayerFS, Mac ARM64 native-local and optional Mac/LayerFS worker boundaries. [Installation](install.md) owns supported packages and operation; [release evidence](releasing.md#release-evidence) records actual source, host and distribution qualification. A passing source check does not establish installation readiness.

## Ownership and platform goals

Server and Runner retain the [architecture's authority and recovery boundaries](architecture.md#remote-execution-and-recovery) across all adapters. Workspace, sandbox and checkpoint implementations never acquire enrollment, model or publication authority.

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

[Project preparation](../tools/project-runtime/README.md) owns locked dependency installation, disabled scripts/hooks and its restricted public-HTTPS broker. Preparation uses the same native process owner, a private staging project/store and HOME/TMP, without host credentials or command-network grants.

Confirm descendants before publishing a ready marker or deleting staging. Unknown cleanup retains staging/receipts, suspends execution and reconciles at startup without replay; a Server result cannot authorize deleting those recovery records. Linux preparation remains independently qualified.

## Operator setup and lifecycle

Use [packaged setup and operation](install.md#connect-a-runner) as the original unprivileged Mac account. Repeated setup preserves registration/journal; source binaries require the separate [development-runtime path](dsh.md#source-runner-setup).

During stopped setup, repeat `--tool-root /absolute/tool/directory` to replace the saved native tool grants, or use `--clear-tool-roots` to revoke them. Omitting both retains previous grants. Grants resolve aliases to existing directories and permit read/execute only. Broad host roots, home ancestors, Runner storage and SSH/cloud/Keychain credential trees are refused. Prefer a specific tool installation, such as a resolved Homebrew Cellar version or Xcode toolchain; a tool upgrade that changes its canonical path requires an explicit grant update. Approved roots contribute their `bin`, `usr/bin` and root directories to the command PATH, covering both Go/Homebrew and Xcode-style toolchain layouts. These grants affect sandbox authority without inheriting the operator's environment or authorizing signing/network services. A changed execution fingerprint can make an earlier session non-resumable; it preserves identity, journal and source data without replay.

[Runner service commands](install.md#choose-operation-mode) own optional background/login operation. Their persistent job is distinct from each command/DSH process owner's coalition. Setup, service installation, immediate start and login startup remain separate choices; changing runtime/tool grants requires a stopped Runner and preserves identity/journal.

## Workspace alternatives

| Combination | Mac-native tools | Linux dependency | Cost and consistency |
| --- | --- | --- | --- |
| Native directory + Seatbelt + local checkpoint | Yes | No | One owned local tree; seal writers before bounded capture; no cross-OS mirror |
| Native directory + Seatbelt + LayerFS Linux worker | Yes | Yes | Native tree plus worker mirror; explicit transfer and generation receipt required |
| Complete Runner in Linux VM + LayerFS | No | Yes | Existing Linux semantics inside a managed guest; VM resources and Linux toolchains |

Workspace and sandbox selection are independent; Linux remains supported. Native-local is the Mac default. The [worker decision](layerfs-worker.md#default-and-deployment-decision) owns the optional backend's capability/cost tradeoff, and [installation](install.md#optional-mac-layerfs-checkpoints) owns stopped backend selection.

Both Mac adapters use the same native workspace, Seatbelt and process owner. The hybrid adds a required, verified complete-tree checkpoint before deriving its separately filtered publication archive. Failures retain work/receipts and suspend the backend without fallback or replay. The [worker contract](layerfs-worker.md) owns Store placement, generation/readback, reconstruction and complete-tree semantics; neither a Linux regular-file proof nor native-local acceptance qualifies another engine/profile.

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

Use the [comparison contract](layerfs-worker.md#cost-comparison-and-default-selection) for paired workloads, repetitions, phase/resource measurements and retained failures. Qualify the operator's actual Mac/toolchain separately from CI; Linux tests, mocked workers and simulated process owners do not establish native/hybrid acceptance. Readiness requires the actual permission/process/workspace self-tests.

### Local Mac and toolchain acceptance

On an actual Apple Silicon Mac, use a Camp checkout with the pinned Node/Go toolchains. Prepare only its owned development dependencies, then run the [local qualification command](../scripts/qualify-native-mac.mjs):

```sh
make install-dsh install-project-runtime
node scripts/qualify-native-mac.mjs
node scripts/qualify-native-mac.mjs --tool-root "$(go env GOROOT)" --command 'go version'
```

The command builds the helper and exercises actual permissions, networking, descendants, dependencies, workspaces, DSH and lifecycle, including an ARM64 Mach-O build with the approved Go SDK. An optional `--command` runs in a fresh private workspace with only the named tool grants, no command networking and confirmed cleanup/capture. Signing/GUI services and operator credentials are not implied grants.

Each invocation reserves a new mode-600 report under `dist/native-mac/`; `--output` refuses replacement. It records source/dirty state, host/runtime identities, command digest and individual pass/fail/skip outcomes, omitting command text/output, private tool paths and credentials. Local failure diagnostics remain private. No enrolled Runner or Server `.env` is loaded. Packaged launchd lifecycle, installation/upgrades and Mac-to-Linux transport have separate acceptance; the interruptible local diagnostic excludes the standalone service fixture.

For a change to these boundaries, update the affected contract and callers together, qualify the actual platform and preserve failed or skipped outcomes. Backend changes need the applicable capture, interruption and identity checks; packaging changes need immutable installation/distribution checks. The [contribution guide](../CONTRIBUTING.md#checks) owns check selection and PR review. Open follow-ups belong in [implementation limits](architecture.md#current-implementation-limits), not a completed implementation sequence.
