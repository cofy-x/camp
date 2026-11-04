# Mac checkpoint worker

This optional adapter keeps a complete native working-tree capture and mirrors it into a native Linux ARM64 LayerFS worker. It does not run an Agent, own a Runner identity, request a model, issue a lease or publish to Camp Server. The [worker contract](../../docs/layerfs-worker.md) owns its consistency, lifecycle and qualification requirements.

Select the adapter explicitly with `runner setup --workspace-backend layerfs-worker` using a matching worker-enabled package. Native-local is the recommended Mac default after the actual-host capability and cost comparison; the worker adds complete-tree checkpoint/history/fork capabilities at additional installation, storage and synchronization cost. See the [default and deployment decision](../../docs/layerfs-worker.md#default-and-deployment-decision). Package availability and qualified environments follow the exact release's evidence; a standalone probe alone does not establish packaged Runner or cross-engine acceptance.

The source pin and SDK version come from [the shared LayerFS pin](../layerfs-runtime/pin.json). [Worker pins](pin.json) select native ARM64 Rust, Node and Docker CLI image manifests independently of the existing Linux amd64 Runner image. Builds use a Camp-owned `git archive` of the exact source commit; shared dependency checkouts remain read-only. Rust dependencies build offline from the locked fetch, with the upstream ARM acceleration configuration intact. No image is pushed by these tools.

The worker uses an inherited bounded pipe through the explicitly selected local Docker socket, with no published control port or automatic restart. Its Store, control receipts and transfer cache live in Linux-local volumes. Neither source nor Store directories are mounted from Mac into the production worker. Mac credentials and ambient environment are excluded. The administrator's Docker authority remains a prerequisite; command isolation must deny that authority to native tools and descendants before Runner readiness.

The [complete-tree profile](../../docs/layerfs-worker.md#workspace-and-checkpoint-boundaries) owns retained names/bytes/modes/links and unsupported metadata/entries. Server publication uses a separately filtered archive. Restore validates the actual destination namespace and never overwrites existing work.

Each operation binds Runner/storage/workspace/attempt identity, Store incarnation, operation ID, generation, snapshot digest and expected parent Commit. Commit IDs retain the SDK's public type prefix. Dispatch states are saved before apply and Commit. Confirmation requires an independently mounted readback and matching head. A Disposable Store receipt explicitly has no crash durability or persistent delivery guarantee; retained Mac captures remain available. Lost connections and incomplete operations prevent automatic mutation or model replay. Reconciliation observes public history and readback rather than guessing success from a Commit ID.

## Source builds and qualification

### Exact package qualification

Use the maintained package campaign on the actual Apple Silicon Mac with an explicitly selected native ARM64 Docker engine. The driver needs Camp's pinned Node and Go toolchains; packages supply their own execution runtimes. It reads no enrolled Runner or Server configuration and makes only deterministic local provider calls.

```sh
node scripts/qualify-mac-worker.mjs \
  --packages /absolute/candidate/packages \
  --baseline v0.1.0-alpha.10 \
  --docker-binary /absolute/path/to/docker \
  --docker-socket /absolute/path/to/docker.sock \
  --engine-profile orbstack \
  --output /absolute/private/mac-worker-qualification.json
```

Replace `--packages` with `--version <published-version>` to qualify that exact public release. `--baseline` is required: select an earlier fixed public version with the matching worker packages; the historical protocol-boundary fixture is not an implicit worker baseline. Public downloads use the fixed Camp repository, published checksums and each package's immutable runtime pin. `--baseline-cache /absolute/private/baseline` can supply an existing worker archive and its adjacent checksum: the directory/files must be canonical, private and owner-controlled; the copied bytes receive full size/hash/checksum verification against freshly acquired fixed-public metadata. This reuses artifact input, never a scenario result, and permits no runtime fallback. The output must be a new file. Every invocation retains a fresh private campaign and its original reports; there is no automatic rerun or replacement of an uncertain result.

To reuse all four independently passing package scenarios, explicitly add `--reuse-report /absolute/original/qualification.json --reuse-directory /absolute/original/campaign`. Their raw report hashes, original Git producer, immutable packages, baseline, toolchain, host, engine identity and complete package driver scope must match. The selected campaign's baseline archive is fully verified against fresh fixed public metadata, avoiding another archive download. A failed parent may supply these four passed subreports; its original error and producer remain recorded. Failed or unknown scenarios are rejected, and restart/recovery run fresh. The online fixture must remain stopped, with verified runtime and no installed service. Reports separate reused model requests from requests executed in the new campaign; a successful reuse campaign contains fourteen historical local requests and two new local requests. Keep the original campaign unchanged. This evidence covers the exact archived worker/API harness; newer UI changes retain their separate main CI acceptance.

Go baseline package/runtime acquisition retains supported HTTP(S)/NO_PROXY settings without inheriting model keys or private Server configuration. For Node public downloads behind that proxy, invoke the driver with the pinned Node's `--use-env-proxy` option. TLS verification stays enabled. The Server and Runner execution fixtures use separate private environments.

The campaign covers online/offline native execution, stopped backup and backend selection, native-local and worker upgrades from the named public version, actual packaged Runner death after checkpoint dispatch, and Store loss with explicit reconstruction. A prior worker uses its own matching image before the current package is installed. An incompatible image is not silently migrated: the stopped fixture retains its backup, captures, receipts and identity, explicitly reconstructs into a new Store epoch, verifies readback, then repeats setup. A setup failure without an image change remains a failure. Initial and post-reconstruction setup attempts retain separate private logs, phase outcomes and durations; upgrade setup time includes both attempts rather than replacing the failed first measurement. Original captures and operation receipts are verified after the transition. Stopped journal checks cover the main file and every present WAL. Only known read-only cleanup inspection may create or remove an empty WAL without changing durable frames; raw observations remain retained. Exact backups preserve sidecar presence and the separately observed volatile SHM coordination index.

Reports bind the Camp version/source, runtime/installer/Server/image digests, SDK pin, shipped controller files, qualification driver and observed engine profile. Linux image proof remains a separate scope. Exact-package Mac evidence is optional for native-local release acceptance; without a matching passing report, the release records Mac worker qualification as pending. For candidate assembly, supply the completed report as `dist/mac-worker-qualification.json`; the collector rejects wrong-source, partial, failed or mismatched reports. Other engines, baselines, paid providers, physical sleep/login, crash durability and VM provisioning remain separate guarantees. The campaign never changes the global Docker context or starts/restarts an existing VM.

Reconstructed fixtures verify that cleanup refuses retained reconstruction history without changing data. Fresh fixtures separately exercise quarantine, restore and permanent purge; a refusal never counts as successful deletion.

The Runner fixtures use the exact archived Server executable as their private harness. Complete Server archive, text/original import and background-service acceptance remain separate gates.

### Packaged runtime cost comparison

Actual package recovery qualification also exercises delayed export release and a controlled release failure with the packaged controller/image. It requires retained capture/journal evidence, no restore publication or operation replay, refusal to reopen unresolved release state, a separately observed stop of the new injected-fault Sandbox, and explicit new-epoch reconstruction with independent readback while the old failure remains intact. Its artificial delay is excluded from performance samples. Existing successful package scopes cannot substitute for this affected recovery proof.

For a focused runtime extraction comparison, use `go test ./apps/server/internal/cli -run '^TestRuntimeArchiveMeasurements$' -count=1 -timeout 30m -v` with `CAMP_TEST_RUNTIME_ARCHIVE` selecting an immutable archive, `CAMP_TEST_RUNTIME_PIN` selecting its JSON `runtimePin` fields (platform, digest, manifest digest and sizes), and `CAMP_RUNTIME_IO_REPORT` selecting a new private JSON report. Three interleaved repetitions compare per-entry full synchronization with staged full synchronization on the same archive and eight I/O slots. Both include the final barrier and independent complete manifest readback; archive prehash and successful fixture cleanup are outside measured phases. This isolates synchronization policy, not a historical executable or cold install. Package setup and native acceptance still use their exact candidate bytes below.

After exact-package qualification passes, keep its private campaign and use its `online.json`, `packages/` directory and completed qualification report. The cost driver derives the controlled runtime from that exact online receipt; it reads no enrolled configuration. Choose the same existing engine and a new private report parent outside application npm ancestry:

```sh
node tools/layerfs-worker/benchmark.mjs \
  --qualification /absolute/private/mac-worker-qualification.json \
  --online-report /absolute/private/qualification-campaign/online.json \
  --packages /absolute/private/qualification-campaign/packages \
  --setup-series both \
  --docker-binary /absolute/path/to/docker \
  --docker-socket /absolute/path/to/docker.sock \
  --engine-profile orbstack \
  --output-base /absolute/private/cost-reports
```

Package mode verifies the retained manifest, controller/SDK pin, used Node/helper binaries and actual image/build/engine identities. It runs three fresh worker/client/Store rounds with the existing four workloads and full, warm, incremental, unchanged, restore and fork phases. The native full-tree arm and streamed worker consume one identical seal per phase; its complete wall time is added explicitly to each arm. The source-only upstream SDK arm is excluded because its host build context is absent from published packages.

Package measurements use their verified `bin/camp-sandbox` for staged restoration synchronization. For source measurements, explicitly set `CAMP_TEST_SANDBOX_HELPER` to the matching newly built absolute helper. Without it, the source comparison retains and labels the older per-file full synchronization policy. A selected helper failure rejects restoration; it never switches policies silently. Raw wire accounting includes coalesced small-file payloads, and worker receipts separate helper input reception and independent readback mount/scan/release costs. Release timings distinguish qualification sampling, public SDK unmount, Sandbox close and journal writes. Unchanged snapshots skip the redundant mutation lifecycle while retaining fresh seals, complete hashes and independent readback; see the [checkpoint contract](../../docs/layerfs-worker.md#sealing-receipts-and-recovery). Compare complete operation times, retaining full hashes, metadata/link checks and confirmed administrative release.

For affected source-only checkpoint work, use `--checkpoint-only --sync-helper /absolute/matching/camp-sandbox --docker-binary /absolute/docker --docker-socket /absolute/docker.sock --engine-profile NAME --output-base /absolute/private/reports`. This requires a clean committed driver, pins the selected local engine and helper, and checks all four workloads and three complete rounds while explicitly excluding the upstream SDK transport. It records no archived-package qualification. Runtime setup, Go Prepare/publication, public installation and model acceptance keep their separate gates; this focused comparison cannot replace release qualification.

Three separate Go processes then measure native-local production Prepare/publication on those same retained inputs using the packaged helper, Node and DSH runtime. Input digests are checked before and after each process and each native sample; complete tuples, fixtures and actual Go PASS events are required. The source scanner modules and actual pinned Node version/hash are recorded and rechecked. Per-sample integrity checks have separate timings outside Prepare/publication and may warm OS caches. Failed or unsupported native workspaces remain retained for inspection; successful workspaces are removed only after confirmed cleanup and verification. Skips, partial reports and uncertain cleanup cannot qualify. This source Go comparison is distinct from performance of the archived Runner binary. A full-tree local restore/fork measurement does not add those features to the native-local Runner.

`--setup-series both` adds three fresh offline installation and repeated-setup samples per backend, keeping native-local and worker series separate. It uses `CAMP_SMOKE_SETUP_ONLY=1` with the existing offline harness: model-free self-tests and confirmed cleanup finish before the report passes, without starting Agent work. The samples measure fresh private runtime/cache setup with existing OS/image caches; they establish no network-download or cold-VM cost. Omit `--setup-series` and `--packages` to retain only the checkpoint comparison and references to the original single setup samples. `--smoke` is one text preflight and does not establish a three-round comparison. Failed and uncertain fixtures remain retained; no campaign restarts a VM or selects a global Docker context in package mode.

### Individual source probes

Use the [repository toolchain](../../CONTRIBUTING.md#development) and run these commands from the Camp root on an actual Apple Silicon Mac with an explicitly selected native ARM64 Docker engine. Build the fixed Linux addon/daemon/image independently of the Linux amd64 Runner:

```sh
node tools/layerfs-worker/install.mjs \
  --source /absolute/path/to/read-only/layerfs \
  --docker-binary /absolute/path/to/docker \
  --docker-socket /absolute/path/to/docker.sock
node --test tools/layerfs-worker/test/*.test.mjs
node tools/layerfs-worker/qualify.mjs .data/worker-arm64-qualification.json \
  --docker-binary /absolute/path/to/docker \
  --docker-socket /absolute/path/to/docker.sock
node tools/layerfs-worker/qualify-mac.mjs
```

The supplied checkout must contain the exact Camp LayerFS pin; it is only read through `git archive`. The explicit Docker pair uses the named local socket without reading or changing the global context, preserves executable alias behavior and excludes ambient Docker/TLS/model settings. The Linux probe checks actual architecture, Linux-local Store placement, FUSE, Commit and remount. Its new-file report passes only after exact owned-resource cleanup; interrupted or failed probes retain private ownership records for inspection. The Mac probe separately captures a complete synthetic working tree, streams it to the selected engine, checks three generations and restores actual checkpoint bytes to a fresh Mac directory. Its report states its actual scope. Ordinary unit tests or this standalone probe do not replace native Runner isolation, real interruption/restart cases or three-round comparative measurements required by [the worker contract](../../docs/layerfs-worker.md).

Omitting `--source` fetches the exact public revision into Camp's private build context. The installer checks the actual Linux engine architecture and collects ARM64 Rust and pinned Docker CLI notices. It does not build in the shared checkout or publish an image.

Additional actual-Mac qualification commands use new private fixtures and deterministic providers:

```sh
node tools/layerfs-worker/qualify-faults.mjs
CAMP_TEST_DOCKER_BINARY=/absolute/path/to/docker CAMP_TEST_DOCKER_SOCKET=/absolute/path/to/docker.sock node tools/layerfs-worker/qualify-recovery.mjs
node tools/layerfs-worker/qualify-contracts.mjs
node tools/layerfs-worker/qualify-apfs.mjs
node tools/layerfs-worker/qualify-upstream.mjs
node tools/layerfs-worker/benchmark.mjs
```

The source benchmark retains three rounds on its selected running engine, rotates all three transport arms and measures a common actual seal separately from each transport. It also writes `native-plan.json`; run `TestNativeLocalComparisonMeasurements` with `CAMP_MAC_COMPARISON_PLAN` set to that file, `CAMP_TEST_DSH_RUNTIME` set to the qualified DSH runtime and `CAMP_MAC_COMPARISON_OUTPUT` set to a new absolute private JSON filename. Confirm an actual Go test pass. The Go command hashes each actual retained input before and after its sample, records that validation time separately, and retains failed workspaces. This measures production native-local Prepare/publication on the identical inputs. The full-tree local profile is a separate comparison, not a claim of production native-local history/restore. Preserve failed and superseded samples, describe cache/background activity and never label an existing VM a cold VM start.

Use `--output-base /absolute/private/report-parent` to retain a new source campaign outside the checkout; the source default parent is `.data`. Add `--smoke` for a one-round text preflight. Each full source campaign rotates the three arms through all three positions; no capture, fork or uncertain mutation is replayed to replace a failed sample.

The APFS qualifier creates and detaches only its own private case-sensitive sparse image. If a diagnostic transport collector invalidates a restore sample, retain that outcome and use `node tools/layerfs-worker/recheck-restore.mjs CAMPAIGN_ROOT DOCKER_SOCKET REPETITION` to read back the retained immutable public forks. This recheck performs no mutation/model replay and does not replace the original sample or become a cold-start measurement. End-to-end checkpoint cost includes the full observed `native-sealing` wall time; its scan/hash/copy submetrics do not include every manifest/receipt synchronization step.

For the affected native boundary/provider tests, supply absolute `CAMP_TEST_SANDBOX_HELPER`, `CAMP_TEST_DSH_RUNTIME`, `CAMP_TEST_LAYERFS_WORKER_RUNTIME` and `CAMP_TEST_DOCKER_SOCKET`, then run the native executor tests with race detection. `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker` selects the hybrid in the deterministic TLS/provider test; omitting it retains the native-local comparison.

Build an optional worker-enabled installation candidate without publishing:

```sh
node scripts/package-worker.mjs \
  --docker-binary /absolute/path/to/docker \
  --docker-socket /absolute/path/to/docker.sock
node scripts/package-runner.mjs --checkpoint-worker
node scripts/build.mjs
```

The matching image descriptor, source and helper hashes are checked before packaging. The ordinary package command remains available without worker support. The release workflow builds the optional image on native Linux ARM64, transfers only its fixed artifact/metadata to the Mac package builder, and stages its checksum with the release. Linux artifact qualification does not replace the actual Mac hybrid acceptance.

Run `scripts/smoke-bundle.mjs online` and `offline` first with the native-local default, then with `CAMP_TEST_WORKSPACE_BACKEND=layerfs-worker`, `CAMP_TEST_DOCKER_BINARY` set to the selected absolute Docker executable, `CAMP_TEST_DOCKER_SOCKET`, and `CAMP_TEST_WORKER_IMAGE` set to the matching optional image archive. The hybrid fixture additionally kills/restarts the actual packaged Runner, switches stopped backends, copies its stopped private backup, restores actual native/worker checkpoint bytes and verifies no model replay. It retains its private fixture and controlled identity for inspection. These are local candidate/mirror checks; public download and cross-version release acceptance remain distinct.

After a hybrid package smoke passes, run `node tools/layerfs-worker/qualify-runner-restart.mjs dist/smoke-layerfs-worker-online.json`. It clones only that stopped synthetic fixture into a new private directory, verifies the installed executable identity and kills the actual Go Runner after an unconfirmed checkpoint dispatch. The restarted Runner must preserve native bytes and identity, report interruption, publish no old result and make no additional model request. The original fixture remains unchanged. Stopped backups must preserve symbolic link spellings; copying a runtime with rewritten absolute dependency links correctly fails its integrity check.

Generated build contexts, images, private fixture data and reports stay ignored. Probes use fresh controlled identities and synthetic data; they never load an enrolled Runner or Server configuration. Failed captures and receipts remain available. Standalone Mac qualification drains its worker but retains its owned Store/home volumes and native captures for recovery inspection; it never prunes unrelated engine resources.

For local disk observations, advisory budgets and explicit quarantine/restore/purge, use [Mac storage inspection](../../docs/install.md#inspect-mac-checkpoint-storage). `storage.mjs` and its Node-only imports ship in the ordinary Mac package without a worker/SDK. [The preview contract](../../docs/layerfs-worker.md#storage-inspection-and-retention-preview) owns bounds, protected references and partial-report behavior. Ordinary sampling and inspection never remove data. Cleanup requires exact stopped-owner plans; only separately confirmed permanent purge deletes quarantined bytes. Referenced history never expires automatically.
