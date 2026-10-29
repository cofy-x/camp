# Install Camp Server and Camp Runner

Camp serves one trusted team from one Server. Ordinary members use a browser; people contributing machines install Camp Runner. The default small installer uses `setup` to download, verify and cache one fixed runtime. The offline package contains that same runtime. Neither path asks members to install or configure DSH, LayerFS, Node or pnpm separately. Provider keys stay on Server.

## Supported deployment

The native Runner package targets **Ubuntu 24.04 amd64**. Its host requires Docker Engine with a local Unix socket and local volume access, `/dev/fuse`, Bubblewrap with working user/PID namespaces, Git and Bash. The fixed native binaries use the platform's system libraries; setup reports missing prerequisites without installing packages or changing host policy.

The LayerFS local-volume backend requires privileged filesystem access. Docker group membership alone is insufficient. Use the same dedicated privileged account for setup, foreground operation and service installation. Native services require root-owned private storage and refuse another account's existing journal. Unprivileged/rootless native execution is unsupported. Treat the Runner operator as a trusted privileged host owner; tool containers receive no Docker control. Ubuntu policy may require an administrator-approved Bubblewrap profile.

Server needs Git and private storage, without Docker, DSH or LayerFS. Its [lightweight image](../Dockerfile) omits native execution and optional PDF/DOCX reader dependencies. For readers on a binary Server deployment, see the [document runtime](../tools/document-runtime/README.md). macOS/Windows browser and Compose usage does not imply native Runner support; a build recipe alone is not validation of another host platform.

## Choose a version

Choose an explicit version from [GitHub Releases](https://github.com/cofy-x/camp/releases), including prereleases. Each release contains a Server archive, Runner installer, digest-addressed runtime, offline package, checksums and sanitized acceptance evidence. The Server image is published in [GHCR](https://github.com/cofy-x/camp/pkgs/container/camp); `release.json` records its immutable registry digest. Verify `SHA256SUMS` before extraction and use that exact version's installation guide.

Early releases use numbered `alpha`, `beta` and `rc` versions. Alpha/beta versions may change APIs, configuration and protocols; an RC is appropriate only when its intended release scope is ready for stabilization. A tag is never moved and asset bytes are never replaced. Fixes receive a new version. Camp does not select `latest`, automatically update an installation or promise compatibility merely because two versions share a `0.x` version prefix.

## Start Server

Extract `camp-server-linux-amd64.tar.gz` and run its `camp`, or build locally with `make build` and use `dist/camp`. The published image contains the same Server binary and dependency licenses. It needs a persistent `/data` mount and explicit port/TLS configuration; it does not include a Runner. For a binary deployment with remote access, provide TLS:

```sh
CAMP_PUBLIC_ORIGIN=https://camp.example.com \
  ./dist/camp serve --addr 0.0.0.0:3443 --data-dir /absolute/path/camp-data \
  --tls-cert /private/server.crt --tls-key /private/server.key \
  --allowed-hosts camp.example.com
```

Read `camp-data/team-setup.token` privately and enter it in browser setup. Create the administrator, configure the model in Settings and invite members. Model configuration is independent of observed connectivity and native execution.

Loopback HTTP is available for development. Remote machine APIs require TLS at Camp, even behind a reverse proxy; preserve Host when proxying to that HTTPS listener. Forwarded headers do not redefine trust. `CAMP_PUBLIC_ORIGIN` fixes browser origin/cookie policy. Runner `--ca-file` adds private CA trust while retaining certificate verification. See [configuration](dsh.md#configuration-and-budgets) for private file selection and [API](api.md#boundary-and-authentication) for origins.

## Connect a Runner

Open **Runners → Connect Runner**, download the installer and checksum, and create a one-use connection code. Ownership comes from that authenticated member; registration alone grants no other Agent access. The UI shows both installer download and setup runtime transfer, with an offline package available on demand.

```sh
sha256sum -c camp-runner-linux-amd64.tar.gz.sha256
tar -xzf camp-runner-linux-amd64.tar.gz
cd camp-runner-linux-amd64
./camp runner setup
```

Interactive setup asks for Server URL, machine name and private storage, then reads the code with terminal echo disabled. It reuses an existing registration without another code. Follow the printed absolute foreground command. Setup does not start a service.

For automation, supply all connection fields and a private code file; explicit `--code-file -` reads one code from stdin. Codes are never command-line values:

```sh
./camp runner setup --server https://camp.example.com --name studio-linux \
  --data-dir /private/camp-runner --code-file /private/connect-code
./camp runner run --data-dir /private/camp-runner
./camp runner status --data-dir /private/camp-runner
```

Setup checks basic host conditions, saves registration, downloads the compiled-in runtime version/digest/size from GitHub Releases, validates archive/files/links, loads the fixed image and performs isolated file/write/export/cleanup self-tests. It uses no model call. Public downloads use a separate client with no Camp credential or private Server CA configuration, allowing only HTTPS redirects within GitHub's release services. Runtime extraction stages privately and publishes its verified cache atomically. Changed digests and unverified native files cannot become executable dependencies. `run` checks the installation but never installs or upgrades it.

For an explicitly staged Server mirror, add `--runtime-source server` to setup. This choice persists across setup retries; `--runtime-source github` selects the public source again. Neither source silently falls back to another version or host. A development installer without a public version uses its enrolled Server. Offline setup always prefers its adjacent matching archive, independently of the saved download source.

Failure retains registration. Fix the reported issue and repeat setup with the same data directory to reuse the identity and verified cache. Explicit setup can quarantine/repair a damaged cache while preserving journal and sessions. A revoked identity needs a new code and directory; it is never silently replaced. `run` can connect with execution unavailable and report Needs attention. Status separates connection observations and their freshness from the last execution check.

Keep the installer at a stable absolute path; runtime cache and private data have separate lifetimes. For offline runtime installation, verify/extract `camp-runner-offline-linux-amd64.tar.gz`, then run the same setup command. It detects the adjacent exact archive and validates it identically. Registration and work still need Server connectivity. The smaller installer moves runtime transfer to setup; it does not remove total download or disk cost.

## Choose operation mode

Foreground `run` is enough. On systemd hosts an operator can separately choose:

```sh
./camp runner service install --data-dir /private/camp-runner
./camp runner service start --data-dir /private/camp-runner
./camp runner service enable-boot --data-dir /private/camp-runner
```

`install` writes/reloads only the managed unit; `start` runs now; `enable-boot` opts into boot startup. `stop`, `status`, `disable-boot` and `remove` are separate actions. Remove stops/disables the managed unit while preserving data. Camp never elevates itself or changes an unrelated service. Revoked credentials or permanent protocol rejection exit with status 78, which the managed service does not restart.

Back in Camp, distinguish **installed, connected, execution ready, model configured and first task succeeded**. Run First Task explicitly creates/shares an owned Agent and requests model-backed file work on the chosen Room. Self-test and fixture success cannot substitute for recorded native task success. Configure Agent targets, grants and personal/team defaults in Agents/Runners; no machine choice is needed for each ordinary message.

An operator can add `--source notes=/absolute/notes` during initial setup. Library selects the alias, captures a new Room snapshot and never writes back to that directory. Quiesce edits when capture must be consistent. See [source-import boundaries](architecture.md#file-and-publication-ownership).

## Data, upgrades and backup

Gracefully stop Server, then back up its **whole** data directory with permissions: SQLite, Git resources, private configuration and immutable model revisions belong together. Separately stop/back up each Runner's configuration, journal and native sessions when those must be retained. Backups contain credentials. LayerFS Stores are disposable and cannot replace the Git/SQLite backup.

Restore into an empty stopped directory using the matching release. Check Library history, review/delivery versions and Runner state before normal use. Never run two Servers on one data directory or two Runners on one journal. Valid queued work can resume through reconciliation; a broken model stream is interrupted. Runner restart cleans uncertain execution and can upload only completely retained output. It never replays an incomplete prompt.

Upgrade with a stopped backup and previous release retained. Verify the new release, repeat Runner setup against the same data, then start explicitly. Reinstall an existing service from the new release to update its launcher path. Existing Team data uses append-only migrations; schema downgrade requires the matching backup. Unsupported pre-Team development databases require a separate directory, not deletion or an invented compatibility migration. Never clear data on failure.

Use Server and Runner from the same release for the supported installation path. Protocol compatibility is an explicit handshake version: incompatible Runners stop with status 78 and do not replay work. Distribution-only changes do not bump that protocol. A protocol match is not a general cross-version data-upgrade guarantee; check the release evidence and migration notes before upgrading. The first public release can establish stopped backup/restore, but has no earlier public release against which to claim an upgrade test.

## Build and publish a release

The [Release workflow](../.github/workflows/release.yml) is manually dispatched on `main` with a fixed version. It verifies that the exact source has passed Source, Browser and Fixture CI and that the tag is unused. A fresh privileged Ubuntu builder installs pinned dependencies, builds all packages, runs Native recovery and online/offline installation checks, tests the Server image, and stages only the reviewed artifact allowlist. A separate job publishes to GitHub Releases and GHCR. Publishing permissions are confined to those jobs; pull requests do not run the privileged release build.

After publication, another fresh Ubuntu host downloads the real public assets without GitHub or Camp credentials, checks their hashes and source identity, runs online/offline Native tasks and stopped backup/restore, then pulls/tests the Server image anonymously. Its report is appended to the release, including failures. A published prerelease with a failed distribution check is not a completed release acceptance. New GHCR packages default to private: the repository owner must make the Camp package public once before anonymous pull can pass; see [GitHub's package visibility guidance](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility). Repository visibility alone does not do this.

Dispatch after the main commit's CI finishes:

```sh
gh workflow run release.yml --repo cofy-x/camp --ref main -f version=v0.1.0-alpha.1
```

For a local rehearsal, release builders use the [repository toolchain](../README.md#local-development) and an isolated clean checkout on the supported privileged host:

```sh
make install install-dsh install-project-runtime install-layerfs
VERSION=v0.1.0-alpha.1 make package-daemon
docker build --target release --build-arg VERSION=v0.1.0-alpha.1 \
  --build-arg SOURCE_COMMIT="$(git rev-parse HEAD)" -t camp-server:v0.1.0-alpha.1 .
make smoke-bundle
node scripts/smoke-server-image.mjs camp-server:v0.1.0-alpha.1
```

`package-daemon` builds the UI/binaries and packages:

- `dist/camp-runner-linux-amd64.tar.gz`: installer with compiled runtime identity.
- `dist/camp-runtime-linux-amd64-<sha256>.tar.gz`: digest-addressed runtime.
- `dist/camp-runner-offline-linux-amd64.tar.gz`: installer plus identical runtime.
- `dist/camp-server-linux-amd64.tar.gz`: standalone Server, embedded Dashboard and licenses.
- Archive checksums, `dist/runner-release.json` and `dist/server-release.json`: fixed source/version, protocol and artifact identities. The published `release.json` also binds the registry digest.

The runtime has its own file/link manifest, licenses and fixed image. Native dependencies stay pinned; only foreign-platform node-pty prebuilds are excluded and recorded. Normalized tar metadata/order keeps unchanged runtime content stable across installer rebuilds. Digests establish integrity, not a publisher signature; use a trusted release channel. Optional document parsing is Server-side and absent from Runner runtime packages.

Runtime packaging precedes installer hashing; the Server then embeds that exact Runner descriptor. The final release catalog can hash the Server without a circular self-hash. A release build refuses dirty source. Unversioned `make package-daemon` creates local development packages; `make build-server-image` builds the development Server image. The `release` Docker target consumes the already verified Server bundle instead of rebuilding a different binary.

Published Servers advertise fixed GitHub downloads without a local package directory. To provide an optional mirror, stage that release's archives/checksums under the configured Server data directory's `packages/`. Copy to temporary names on the same filesystem, verify, then rename atomically; publish `runner-release.json` **last**. A published Server accepts only its compiled-in descriptor for installer downloads; an older/different staged descriptor cannot redirect the UI. Retain old digest-addressed runtimes while their installers remain in use. Never change bytes under a digest. Staging does not execute or update any existing Runner.

Default installer metadata requires its runtime also staged at the declared size. Offline availability is independent. Installer/offline downloads use browser authentication; exact runtime downloads use enabled Runner credentials and reject browser cookies. Host/TLS policy, Range handling and idle transfer bounds apply.

`make smoke-bundle` tests the explicit Server mirror and offline paths. `node scripts/smoke-bundle.mjs github` and `github-offline` require published assets and test public distribution without Server staging. All modes relocate the installation, remove host Node from the Runner PATH, test repeat setup, native execution, configured checks, binary transfer, one publication, human approval/delivery, stopped backup/restore and cleanup with a deterministic provider. Local execution alone does not establish fresh-host portability or real-provider behavior. Use the [validation layers](../README.md#checks-and-maintenance) and report which actually ran; service tests use a controlled substitute unless an operator separately chooses a real installation.

## Release evidence

Attach a sanitized acceptance report to each release, or link retained CI artifacts from its release notes. Record the source commit and any local modifications, artifact digests, host OS/architecture, runtime pins, commands/scenarios actually run, and passes, failures, skips or untested scope. Report source, browser, fixture, native, fresh-host installation and real-provider results separately. Keep evidence retrievable for the supported lifetime of that release; a temporary CI link alone is insufficient.

Keep raw logs, screenshots and provider ledgers private in ignored directories. Publish only reviewed evidence without credentials or private team data. Maintained guides describe guarantees and reproducible procedures; release artifacts retain the dated results.
