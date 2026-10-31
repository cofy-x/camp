# Install Camp Server and Camp Runner

Camp serves one trusted team from one Server. Ordinary members use a browser; people contributing machines install Camp Runner. The default small installer uses `setup` to download, verify and cache one fixed runtime. The offline package contains that same runtime. Neither path asks members to install or configure DSH, LayerFS, Node or pnpm separately. Provider keys stay on Server.

## Supported deployment

The native Runner packages target **Ubuntu 24.04 amd64** and **macOS 15+ Apple Silicon**. By default Mac uses a native local workspace and Seatbelt, without Docker, Linux VM, FUSE or root. The optional checkpoint worker adds a Linux engine dependency while keeping commands native. Use the original unprivileged account for setup and operation; Intel Mac supports Server only. Native tool installations require explicit read/execute grants described in the [native Runner contract](native-runner.md#operator-setup-and-lifecycle).

The Linux Runner uses LayerFS. Its host requires Docker Engine with a local Unix socket and local volume access, `/dev/fuse`, Bubblewrap with working user/PID namespaces, Git and Bash. The fixed native binaries use the platform's system libraries; setup reports missing prerequisites without installing packages or changing host policy.

Runner discovers the selected local engine's volume directory through Docker's system-info API, including custom Docker data directories. That directory must be directly accessible to the privileged Runner account; a remote Docker endpoint cannot provide this execution environment.

The LayerFS local-volume backend requires privileged filesystem access. Docker group membership alone is insufficient. Use the same dedicated privileged account for setup, foreground operation and service installation. Linux native services require root-owned private storage and refuse another account's existing journal. Unprivileged/rootless Linux execution is unsupported. Treat the Runner operator as a trusted privileged host owner; tool containers receive no Docker control. Ubuntu policy may require an administrator-approved Bubblewrap profile.

Server packages target Linux amd64 and macOS arm64/amd64. Server needs Git and private storage, without Docker, DSH or LayerFS. Its [lightweight image](../Dockerfile) omits native execution and optional PDF/DOCX reader dependencies. For readers on a Linux binary Server deployment, see the [document runtime](../tools/document-runtime/README.md). macOS Server supports discussion, uploads, versioned text files, human review and independent supported Runners. PDF/DOCX text preparation and controlled Server-host directory import currently require Linux; originals remain downloadable. Browser access from Windows requires no CLI. Mac/Windows browser and Compose usage does not imply native Runner support.

## Choose a version

Choose an explicit version from [GitHub Releases](https://github.com/cofy-x/camp/releases), including prereleases. Each release contains a Server archive, Runner installer, digest-addressed runtime, offline package, checksums and sanitized acceptance evidence. The Server image is published in [GHCR](https://github.com/cofy-x/camp/pkgs/container/camp); `release.json` records its immutable registry digest. Verify `SHA256SUMS` before extraction and use that exact version's installation guide.

Early releases use numbered `alpha`, `beta` and `rc` versions. Alpha/beta versions may change APIs, configuration and protocols; an RC is appropriate only when its intended release scope is ready for stabilization. A tag is never moved and asset bytes are never replaced. Fixes receive a new version. Camp does not select `latest`, automatically update an installation or promise compatibility merely because two versions share a `0.x` version prefix.

## Start Server

### Go installation

Install the fixed release entrypoint using the [pinned Go toolchain](../CONTRIBUTING.md#development):

```sh
go install github.com/cofy-x/camp@v0.1.0-alpha.5
camp
```

Put Go's binary installation directory on PATH. This entrypoint downloads the same complete Server archive as a manual installation; it does not compile an API-only development Server or require Node/pnpm. `camp runner ...` selects the matching native Runner installer instead.

The launcher derives its immutable version from Go's build information. It never selects `latest`, substitutes another platform or advances an existing installation. Downloads use system TLS and only GitHub release redirects, without Camp credentials. Archive/checksum names are fixed by version, role and platform. Checksums establish release-channel integrity, not a separate publisher signature.

Complete packages, including licenses, live under the OS user cache directory's `camp/releases/<version>/`. Installation uses a private staging directory and lock; only a bounded, checksum-verified archive with safe regular files can become runnable. Subsequent invocations verify the cached executable without a network request. Missing or damaged cache files trigger repair of that same version; failed transfers retain the prior cache and are retried by repeating the command. Server data and Runner identity/journals remain outside this cache. Deleting a release cache does not delete team data, but stop any service using that cached executable before removing it.

`camp help` and `camp version` work without downloads or private configuration. A source/pseudo-version entrypoint refuses to guess a public release: developers use `make build`. Downloads support cancellation and report controlled network errors without signed URLs or transport credentials. Offline hosts use the standalone archives; this launcher is an online installation channel.

### Homebrew installation

On Apple Silicon and Intel Mac, the maintained [formula](../Formula/camp.rb) installs the complete Server and Git from fixed, checksum-verified release archives. It requires neither Go nor Node/pnpm:

```sh
brew tap cofy-x/camp https://github.com/cofy-x/camp
brew install cofy-x/camp/camp
camp
```

The explicit repository URL is required because this tap lives in the Camp repository. The fully qualified formula selects Camp's own package. The formula pins `v0.1.0-alpha.5`; release maintainers update it in a reviewed PR after public verification. `brew upgrade cofy-x/camp/camp` is an explicit installation update; stop Camp and retain a full backup first. Homebrew keeps team data outside its Cellar and never starts a Camp service. On Apple Silicon, run `camp runner setup` directly: its compiled catalog pins the native runtime without a second installer download. Intel Mac connects a separate supported Runner.

### Standalone Server

Run `camp doctor` to inspect the selected local address, Git and private storage without creating data or reading model credentials. It accepts `--addr`, `--data-dir` and paired TLS path options with the same environment/default resolution as Server. It checks local prerequisites, not provider connectivity or Runner execution. `camp open` opens the resolved browser address with the OS browser command; `camp open --print` only prints it on headless hosts. `CAMP_PUBLIC_ORIGIN` takes precedence for the browser address; wildcard bind addresses map to loopback. Pass the same explicit address/TLS options used for startup when they are not in the process environment.

Starting `camp` prints the browser address, data location and private setup-token file path. The token itself is never printed or placed in a URL. Opening the browser is explicit and no service is enabled. After creating the team, use **Set Up Agent Work…** in an empty Room or **Help → Getting Ready** to follow model setup, Runner connection and first-task selection. Configure model settings in the browser; no model key belongs in shell arguments or Runner configuration.

Extract the matching `camp-server-linux-amd64.tar.gz`, `camp-server-darwin-arm64.tar.gz` (Apple Silicon) or `camp-server-darwin-amd64.tar.gz` (Intel Mac), then run its `camp`. Source developers use `make build` and `dist/camp`. Keep data outside the extracted installation. The published Linux image contains the same Linux Server binary and dependency licenses. It needs a persistent `/data` mount and explicit port/TLS configuration; it does not include a Runner. For a binary deployment with remote access, provide TLS:

```sh
CAMP_PUBLIC_ORIGIN=https://camp.example.com:3443 \
  ./camp serve --addr 0.0.0.0:3443 --data-dir /absolute/path/camp-data \
  --tls-cert /private/server.crt --tls-key /private/server.key \
  --allowed-hosts camp.example.com
```

For this direct listener, open `https://camp.example.com:3443` and give Runner that same origin. The explicit port is part of Host/origin validation. The CLI defaults to `127.0.0.1:3001`; the image/example uses `3443`. Neither makes external port 443 implicit. To expose `https://camp.example.com` instead, explicitly map external 443 to the HTTPS listener (for example Docker `-p 443:3443`), or configure an HTTPS reverse proxy preserving the original Host and TLS at Camp, and set that external origin.

Read `camp-data/team-setup.token` privately and enter it in browser setup. Create the administrator, configure the model in Settings and invite members. Model configuration is independent of observed connectivity and native execution.

Loopback HTTP is available for development. Remote machine APIs require TLS at Camp, even behind a reverse proxy; preserve Host when proxying to that HTTPS listener. Forwarded headers do not redefine trust. `CAMP_PUBLIC_ORIGIN` fixes browser origin/cookie policy. Runner `--ca-file` adds private CA trust while retaining certificate verification. See [configuration](dsh.md#configuration-and-budgets) for private file selection and [API](api.md#boundary-and-authentication) for origins.

## Connect a Runner

Open **Runners → Connect Runner**, choose the execution platform, download its installer and checksum, and create a one-use connection code. Ownership comes from that authenticated member; registration alone grants no other Agent access. The UI shows both installer download and setup runtime transfer, with an offline package available on demand.

```sh
sha256sum -c camp-runner-linux-amd64.tar.gz.sha256
tar -xzf camp-runner-linux-amd64.tar.gz
cd camp-runner-linux-amd64
./camp runner setup
```

On Mac, verify with `shasum -a 256 -c camp-runner-darwin-arm64.tar.gz.sha256`, extract that archive and run the same `./camp runner setup` command as the unprivileged operator. A complete published Server/Homebrew binary also supports `camp runner setup` using the embedded platform runtime pin.

Interactive setup asks for Server URL, machine name and private storage, then reads the code with terminal echo disabled. It reuses an existing registration without another code. Follow the printed absolute foreground command. Setup does not start a service.

For automation, supply all connection fields and a private code file; explicit `--code-file -` reads one code from stdin. Codes are never command-line values:

```sh
./camp runner setup --server https://camp.example.com:3443 --name studio-linux \
  --data-dir /private/camp-runner --code-file /private/connect-code
./camp runner run --data-dir /private/camp-runner
./camp runner status --data-dir /private/camp-runner
```

For a new Runner, setup checks the supported platform, execution account and basic host prerequisites before reading the connection code or registering. It then saves registration, downloads the compiled-in runtime version/digest/size from GitHub Releases, validates archive/files/links, loads the fixed image on Linux and performs isolated file/write/export/cleanup self-tests. It uses no model call. Public downloads use a separate client with no Camp credential or private Server CA configuration, allowing only HTTPS redirects within GitHub's release services. Failures identify a controlled DNS, TLS, connection, timeout, cancellation, redirect or interrupted-transfer class without exposing signed URLs or transport credentials. Runtime extraction stages privately and publishes its verified cache atomically. Changed digests and unverified native files cannot become executable dependencies. `run` checks the installation but never installs or upgrades it.

For an explicitly staged Server mirror, add `--runtime-source server` to setup. This choice persists across setup retries; `--runtime-source github` selects the public source again. Neither source silently falls back to another version or host. A development installer without a public version uses its enrolled Server. Offline setup always prefers its adjacent matching archive, independently of the saved download source.

Failure retains registration. Fix the reported issue and repeat setup with the same data directory to reuse the identity and verified cache. Explicit setup can quarantine/repair a damaged cache while preserving journal and sessions. A revoked identity needs a new code and directory; it is never silently replaced. `run` can connect with execution unavailable and report Needs attention. Status separates connection observations and their freshness from the last execution check.

Use the same execution account and absolute private directory for setup, foreground operation and services: privileged on Linux, unprivileged on Mac. A host failure before enrollment creates no Runner identity. If an earlier installation registered under another account, stop it and inspect its saved state under that account; do not change ownership of its journal or silently enroll a replacement. If replacement is intended, explicitly revoke the old Runner in Camp and use a new code and separate root-owned directory.

Keep the installer at a stable absolute path; runtime cache and private data have separate lifetimes. For offline runtime installation, verify/extract `camp-runner-offline-linux-amd64.tar.gz`, (or `camp-runner-offline-darwin-arm64.tar.gz` on Mac), then run the same setup command. It detects the adjacent exact archive and validates it identically. Registration and work still need Server connectivity. The smaller installer moves runtime transfer to setup; it does not remove total download or disk cost.

### Optional Mac LayerFS checkpoints

Native-local is the recommended Mac default. For complete working-tree checkpoints, history and forks, use a matching worker-enabled Mac runtime and its separately checksummed `camp-layerfs-worker-linux-arm64-<SHA256>.tar.gz` artifact. Check that exact release's evidence for availability and qualified engines; local candidate packaging does not establish public distribution. The option requires a running native ARM64 rootful Docker engine, such as the qualified OrbStack engine. Store data stays in Linux-local volumes; no Mac folder is mounted as the Store. Host FUSE, a separate Runner inside Linux and model keys on Runner are unnecessary. The [default decision](layerfs-worker.md#default-and-deployment-decision) explains the capability/cost tradeoff; [Linux environment provisioning](layerfs-worker.md#linux-environment-provisioning) distinguishes a selected engine from a managed-VM feature.

Stop the Runner before selecting a backend. Keep its existing private data directory and registration. For OrbStack, for example:

```sh
./camp runner setup --data-dir /absolute/private/runner \
  --workspace-backend layerfs-worker \
  --docker-socket "$HOME/.orbstack/run/docker.sock" \
  --docker-binary /usr/local/bin/docker \
  --worker-image /absolute/downloads/camp-layerfs-worker-linux-arm64-SHA256.tar.gz
```

Setup verifies the fixed archive and actual ARM64 image, saves the absolute Docker invocation path and runs native isolation plus checkpoint/restore self-tests. Preserve a Docker entry symlink's name: OrbStack dispatches its multicall executable using that name. Omit `--worker-image` when the matching image is already present. Omit `--workspace-backend` during later setup to retain the saved choice; choose `native-local` explicitly to return to that backend. Neither selection resets identity, journal or captures. Runtime/Store fingerprint changes can invalidate session resume eligibility while retaining session data. Setup never starts a VM or service implicitly.

The complete checkpoint includes `.git`, ignored dependencies, supported permissions, directories and link topology. Server publication remains a separately filtered regular-file archive. The [worker contract](layerfs-worker.md) defines unsupported metadata/files, APFS name conflicts and performance/recovery boundaries. A selected worker's failure blocks required publication and retains native work; it does not silently switch backends. Checkpoints use a Disposable Store and are not durable Server delivery.

Inspect retained metadata without starting a worker, then stop the Runner before observing or restoring an operation:

```sh
./camp runner checkpoint status --data-dir /absolute/private/runner
./camp runner checkpoint observe --data-dir /absolute/private/runner --operation OPERATION_ID
./camp runner checkpoint restore --data-dir /absolute/private/runner --operation OPERATION_ID --parent /absolute/private/restores
```

The restore parent must exist, belong to the operator and have mode 700; restoration creates a new child and never overwrites an existing tree. `restore --from-native` explicitly restores retained native bytes when the worker is unavailable and reports that no current worker verification was performed. After Store loss or an incompatible worker upgrade, `reconstruct --operation OPERATION_ID` creates a new Store epoch and new operation, verifies readback, and selects it while retaining old captures/uncertain receipts. If selection was interrupted, use `observe-reconstruction --recovery RECOVERY_ID`. These actions neither replay tools/models nor publish to Server. Status reports its last recorded engine/SDK/image identities as historical observations, not current readiness.

Back up the whole stopped Runner directory, including captures and operation receipts. Relative capture references support relocation of that backup; a Linux Disposable Store is still separate and can require explicit reconstruction. Keep the previous runtime and backup while upgrading. Existing absolute-only experimental receipts that escape the restored home require explicit inspection rather than guessed paths.

## Choose operation mode

Foreground `run` is enough. On systemd hosts an operator can separately choose:

```sh
./camp runner service install --data-dir /private/camp-runner
./camp runner service start --data-dir /private/camp-runner
./camp runner service enable-boot --data-dir /private/camp-runner
```

`install` writes/reloads only the managed unit; `start` runs now; `enable-boot` opts into boot startup. `stop`, `status`, `disable-boot` and `remove` are separate actions. Remove stops/disables the managed unit while preserving data. Camp never elevates itself or changes an unrelated service. Revoked credentials or permanent protocol rejection exit with status 78, which the managed service does not restart.

On Mac, the same `service install`, `start`, `stop`, `status` and `remove` commands manage a private launchd job. `enable-login` and `disable-login` separately opt into or out of login startup. Installation starts nothing. A stopped or failed process remains stopped until explicitly started; network reconnection is owned by the running daemon. Removal preserves identity, journal and resource data.

When stopped, repeat setup with `--tool-root /absolute/tool/version` to replace saved native read/execute grants, or `--clear-tool-roots` to revoke them. Omission retains existing grants. Use narrow canonical installations, such as a specific Homebrew Cellar version; home, credential and Runner storage trees are refused. A grant does not authorize tool credentials, signing services, GUI automation or network. See [tool and lifecycle boundaries](native-runner.md#operator-setup-and-lifecycle).

For retained qualification of an operator's Apple Silicon Mac and approved toolchain, use the [local Mac acceptance command](native-runner.md#local-mac-and-toolchain-acceptance) from a source checkout. It creates fresh test storage and a private report without loading an enrolled Runner. Installed package setup continues to perform its own model-free readiness check.

Back in Camp, distinguish **installed, connected, execution ready, model configured and first task succeeded**. Run First Task explicitly creates/shares an owned Agent and requests model-backed file work on the chosen Room. Self-test and fixture success cannot substitute for recorded native task success. Configure Agent targets, grants and personal/team defaults in Agents/Runners; no machine choice is needed for each ordinary message.

An operator can add `--source notes=/absolute/notes` during initial setup. Repeating setup accepts identical source flags but rejects a different source map. To change an existing Runner's sources, stop it, preserve a private backup of `daemon.json`, edit only its `sources` map from aliases to absolute directories, then restart with the same data directory. Keep identity, credentials and journal unchanged. Library selects the alias, captures a new Room snapshot and never writes back to that directory. Quiesce edits when capture must be consistent. See [source-import boundaries](architecture.md#file-and-publication-ownership).

## Data, upgrades and backup

Check the exact release's [upgrade qualification](releasing.md#upgrade-qualification) before changing an installation. Acceptance names its tested Server/Runner baseline and platform; a passing Server data upgrade does not establish Mac worker-image upgrade compatibility.

Gracefully stop Server, then back up its **whole** data directory with permissions: SQLite, Git resources, private configuration and immutable model revisions belong together. Separately stop/back up each Runner's configuration, journal and native sessions when those must be retained. Backups contain credentials. LayerFS Stores are disposable and cannot replace the Git/SQLite backup.

Restore into an empty stopped directory using the matching release. Check Library history, review/delivery versions and Runner state before normal use. Never run two Servers on one data directory or two Runners on one journal. Valid queued work can resume through reconciliation; a broken model stream is interrupted. Runner restart cleans uncertain execution and can upload only completely retained output. It never replays an incomplete prompt.

Upgrade with a stopped backup and previous release retained. Verify the new release, repeat Runner setup against the same data, then start explicitly. Reinstall an existing service from the new release to update its launcher path. Existing Team data uses append-only migrations; schema downgrade requires the matching backup. Unsupported pre-Team development databases require a separate directory, not deletion or an invented compatibility migration. Never clear data on failure.

Use Server and Runner from the same release for the supported installation path. Protocol compatibility is an explicit handshake version: incompatible Runners stop with status 78 and do not replay work. Distribution-only changes do not bump that protocol. A protocol match is not a general cross-version data-upgrade guarantee; check the named baseline, release evidence and migration notes before upgrading.

## Stage a package mirror

Published Servers advertise fixed GitHub downloads without a local package directory. To provide an optional mirror, stage that release's archives/checksums under the configured Server data directory's `packages/`. Copy to temporary names on the same filesystem, verify, then rename atomically; publish `runner-release.json` **last**. A published Server accepts only its compiled-in descriptor for installer downloads; an older/different staged descriptor cannot redirect the UI. Retain old digest-addressed runtimes while their installers remain in use. Never change bytes under a digest. Staging does not execute or update any existing Runner.

Default installer metadata requires its runtime also staged at the declared size. Offline availability is independent. Installer/offline downloads use browser authentication; exact runtime downloads use enabled Runner credentials and reject browser cookies. Host/TLS policy, Range handling and idle transfer bounds apply.

## Build and publish a release

Maintainers use [Releasing](releasing.md) for package assembly, reviewed-main publication, public verification and the tap/quick-start update. Installing or staging packages does not publish a release.

## Release evidence

Read the exact release's acceptance and distribution reports before selecting an optional backend or upgrading. Source, browser, fixture, native isolation, public installation and paid-provider results establish separate guarantees; a report for another source/runtime does not automatically apply. [Releasing](releasing.md#release-evidence) owns how maintainers retain and sanitize that evidence.
