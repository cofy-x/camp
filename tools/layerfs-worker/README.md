# Mac checkpoint worker

This optional adapter keeps a complete native working-tree capture and mirrors it into a native Linux ARM64 LayerFS worker. It does not run an Agent, own a Runner identity, request a model, issue a lease or publish to Camp Server. The [worker contract](../../docs/layerfs-worker.md) owns its consistency, lifecycle and qualification requirements.

Select the adapter explicitly with `runner setup --workspace-backend layerfs-worker` using a matching worker-enabled package. Native-local is the recommended Mac default after the actual-host capability and cost comparison; the worker adds complete-tree checkpoint/history/fork capabilities at additional installation, storage and synchronization cost. See the [default and deployment decision](../../docs/layerfs-worker.md#default-and-deployment-decision). Package availability and qualified environments follow the exact release's evidence; a standalone probe alone does not establish packaged Runner or cross-engine acceptance.

The source pin and SDK version come from [the shared LayerFS pin](../layerfs-runtime/pin.json). [Worker pins](pin.json) select native ARM64 Rust, Node and Docker CLI image manifests independently of the existing Linux amd64 Runner image. Builds use a Camp-owned `git archive` of the exact source commit; shared dependency checkouts remain read-only. Rust dependencies build offline from the locked fetch, with the upstream ARM acceleration configuration intact. No image is pushed by these tools.

The worker uses an inherited bounded pipe through the explicitly selected local Docker socket, with no published control port or automatic restart. Its Store, control receipts and transfer cache live in Linux-local volumes. Neither source nor Store directories are mounted from Mac into the production worker. Mac credentials and ambient environment are excluded. The administrator's Docker authority remains a prerequisite; command isolation must deny that authority to native tools and descendants before Runner readiness.

Complete captures retain ordinary file bytes, Unix permissions, directories, `.git`, ignored dependencies, opaque symlinks and internal hardlink groups. An external hardlink alias, socket, FIFO, device, special permission bits, non-UTF-8 name or backslash in a name is explicitly unsupported. This profile does not capture owners, ACLs, extended attributes, timestamps, sparse allocation or platform file flags. It represents the working-tree content and supported permissions, independently of Server publication filtering. Actual destination name creation detects case and Unicode collisions before installing restored bytes or links; existing destinations are never overwritten.

Each operation binds Runner/storage/workspace/attempt identity, Store incarnation, operation ID, generation, snapshot digest and expected parent Commit. Commit IDs retain the SDK's public type prefix. Dispatch states are saved before apply and Commit. Confirmation requires an independently mounted readback and matching head. A Disposable Store receipt explicitly has no crash durability or persistent delivery guarantee; retained Mac captures remain available. Lost connections and incomplete operations prevent automatic mutation or model replay. Reconciliation observes public history and readback rather than guessing success from a Commit ID.

## Source builds and qualification

Use the [repository toolchain](../../CONTRIBUTING.md#development) and run these commands from the Camp root on an actual Apple Silicon Mac with an explicitly selected native ARM64 Docker engine. Build the fixed Linux addon/daemon/image independently of the Linux amd64 Runner:

```sh
node tools/layerfs-worker/install.mjs --source /absolute/path/to/read-only/layerfs
node --test tools/layerfs-worker/test/*.test.mjs
node tools/layerfs-worker/qualify.mjs .data/worker-arm64-qualification.json
node tools/layerfs-worker/qualify-mac.mjs
```

The supplied checkout must contain the exact Camp LayerFS pin; it is only read through `git archive`. The Linux probe checks actual architecture, Linux-local Store placement, FUSE, Commit and remount. The Mac probe separately captures a complete synthetic working tree, streams it to the selected engine, checks three generations and restores actual checkpoint bytes to a fresh Mac directory. Its report states its actual scope. Ordinary unit tests or this standalone probe do not replace native Runner isolation, real interruption/restart cases or three-round comparative measurements required by [the worker contract](../../docs/layerfs-worker.md).

Omitting `--source` fetches the exact public revision into Camp's private build context. The installer checks the actual Linux engine architecture and collects ARM64 Rust and pinned Docker CLI notices. It does not build in the shared checkout or publish an image.

Additional actual-Mac qualification commands use new private fixtures and deterministic providers:

```sh
node tools/layerfs-worker/qualify-faults.mjs
node tools/layerfs-worker/qualify-recovery.mjs
node tools/layerfs-worker/qualify-contracts.mjs
node tools/layerfs-worker/qualify-apfs.mjs
node tools/layerfs-worker/qualify-upstream.mjs
node tools/layerfs-worker/benchmark.mjs
```

The benchmark retains three rounds on the selected running engine, rotates transport order and measures a common actual seal separately from each transport. It also writes `native-plan.json`; run `TestNativeLocalComparisonMeasurements` with `CAMP_MAC_COMPARISON_PLAN` set to that file and `CAMP_MAC_COMPARISON_OUTPUT` set to a new private JSON filename. This measures production native-local Prepare/publication on the identical inputs. The full-tree local profile is a separate comparison, not a claim of production native-local history/restore. Preserve failed and superseded samples, describe cache/background activity and never label an existing VM a cold VM start.

Use `--output-base /absolute/private/report-parent` to retain a new campaign outside the checkout; the default parent is `.data`. Add `--smoke` for a one-round text preflight. Each full campaign rotates the three arms through all three positions; no capture, fork or uncertain mutation is replayed to replace a failed sample.

The APFS qualifier creates and detaches only its own private case-sensitive sparse image. If a diagnostic transport collector invalidates a restore sample, retain that outcome and use `node tools/layerfs-worker/recheck-restore.mjs CAMPAIGN_ROOT DOCKER_SOCKET REPETITION` to read back the retained immutable public forks. This recheck performs no mutation/model replay and does not replace the original sample or become a cold-start measurement. End-to-end checkpoint cost includes the full observed `native-sealing` wall time; its scan/hash/copy submetrics do not include every manifest/receipt synchronization step.

For the affected native boundary/provider tests, supply absolute `CAMP_TEST_SANDBOX_HELPER`, `CAMP_TEST_DSH_RUNTIME`, `CAMP_TEST_LAYERFS_WORKER_RUNTIME` and `CAMP_TEST_DOCKER_SOCKET`, then run the native executor tests with race detection. `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker` selects the hybrid in the deterministic TLS/provider test; omitting it retains the native-local comparison.

Build an optional worker-enabled installation candidate without publishing:

```sh
node scripts/package-worker.mjs
node scripts/package-runner.mjs --checkpoint-worker
node scripts/build.mjs
```

The matching image descriptor, source and helper hashes are checked before packaging. The ordinary package command remains available without worker support. The release workflow builds the optional image on native Linux ARM64, transfers only its fixed artifact/metadata to the Mac package builder, and stages its checksum with the release. Linux artifact qualification does not replace the actual Mac hybrid acceptance.

Run `scripts/smoke-bundle.mjs online` and `offline` first with the native-local default, then with `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker`, `CAMP_TEST_DOCKER_BINARY` set to the selected absolute Docker executable, `CAMP_TEST_DOCKER_SOCKET`, and `CAMP_TEST_WORKER_IMAGE` set to the matching optional image archive. The hybrid fixture additionally kills/restarts the actual packaged Runner, switches stopped backends, copies its stopped private backup, restores actual native/worker checkpoint bytes and verifies no model replay. It retains its private fixture and controlled identity for inspection. These are local candidate/mirror checks; public download and cross-version release acceptance remain distinct.

After a hybrid package smoke passes, run `node tools/layerfs-worker/qualify-runner-restart.mjs dist/smoke-layerfs-worker-online.json`. It clones only that stopped synthetic fixture into a new private directory, verifies the installed executable identity and kills the actual Go Runner after an unconfirmed checkpoint dispatch. The restarted Runner must preserve native bytes and identity, report interruption, publish no old result and make no additional model request. The original fixture remains unchanged. Stopped backups must preserve symbolic link spellings; copying a runtime with rewritten absolute dependency links correctly fails its integrity check.

Generated build contexts, images, private fixture data and reports stay ignored. Probes use fresh controlled identities and synthetic data; they never load an enrolled Runner or Server configuration. Failed captures and receipts remain available. Standalone Mac qualification drains its worker but retains its owned Store/home volumes and native captures for recovery inspection; it never prunes unrelated engine resources.
