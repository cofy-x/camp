# Build, verify and publish Camp

This guide owns release assembly, publication gates and retained evidence. [Installation](install.md) owns operator setup, services, upgrades and backup; [CONTRIBUTING](../CONTRIBUTING.md) owns local development. Release results belong with their fixed-version [GitHub Release](https://github.com/cofy-x/camp/releases), rather than in a growing guide changelog.

## Release pipeline

The [Release workflow](../.github/workflows/release.yml) binds one fixed version to an exact reviewed `main` commit whose Source, Browser and Fixture CI passed. The tag must be unused. A native Linux ARM64 builder qualifies and packages the optional checkpoint image. The Mac ARM64 builder imports that fixed descriptor and builds the native runtime and installers; it does not run the Linux worker. The privileged Ubuntu builder combines the matching platform contributions, builds Linux packages and the Server catalog, checks native recovery and installation, and stages only the reviewed artifact allowlist. Actual Mac hosts verify Server archives and native Runner installation before publication. Separate jobs publish to GitHub Releases/GHCR and retain public verification. Pull requests do not publish releases.

Maintainers can manually dispatch the same workflow on a trusted development branch with `validate-only=true` to check Native packages before merging. This mode has read-only repository permissions, retains CI artifacts, and creates no tag, Release or registry image. Its report explicitly leaves Source/Browser/Fixture acceptance and public distribution ungranted. Publishing still requires the normal mode on reviewed `main`.

After publication, fresh Linux and Mac hosts download the real public assets without GitHub or Camp credentials, check hashes/source identity and exercise online/offline native installation. Linux also checks the named Runner upgrade and pulls/tests the Server image anonymously. Tagged Go installation is verified on Linux and both Mac Server architectures. Their reports are appended to the release, including failures. A published prerelease with a failed distribution check is not a completed release acceptance. New GHCR packages default to private: the repository owner must make the Camp package public once before anonymous pull can pass; see [GitHub's package visibility guidance](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility). Repository visibility alone does not do this.

## Dispatch and rehearsal

Replace `vX.Y.Z` below with a new, unused release tag. Dispatch publication only after the selected main commit's CI passes and publication is authorized:

```sh
VERSION=vX.Y.Z
gh workflow run release.yml --repo cofy-x/camp --ref main -f version="$VERSION"
```

A trusted development branch can rehearse the package pipeline without publication:

```sh
VERSION=vX.Y.Z
gh workflow run release.yml --repo cofy-x/camp --ref your-branch \
  -f version="$VERSION" -f validate-only=true
```

Inspect the run's exact source and retained reports before taking the next action. A rehearsal is not public distribution acceptance. A failed post-publication check remains a failed outcome; append diagnostic evidence without moving the tag or replacing existing asset bytes.

For local development packaging, use the [contribution commands](../CONTRIBUTING.md#build) and the owning [Mac worker tooling](../tools/layerfs-worker/README.md#source-builds-and-qualification). Local package checks do not publish or promote private candidates into an existing release. The workflow is the authority for assembling a multi-platform release.

## Artifacts and provenance

The release assembles these platform contributions:

- `dist/camp-runner-linux-amd64.tar.gz`: installer with compiled runtime identity.
- `dist/camp-runtime-linux-amd64-<sha256>.tar.gz`: digest-addressed runtime.
- `dist/camp-runner-offline-linux-amd64.tar.gz`: installer plus identical runtime.
- `dist/camp-server-linux-amd64.tar.gz`: standalone Server, embedded Dashboard and licenses.
- `dist/camp-server-darwin-arm64.tar.gz` and `dist/camp-server-darwin-amd64.tar.gz`: complete macOS Servers with the same fixed Runner descriptor.
- `dist/camp-runner-darwin-arm64.tar.gz`, its digest-addressed native runtime and `dist/camp-runner-offline-darwin-arm64.tar.gz`: unprivileged Mac execution.
- `dist/camp-layerfs-worker-linux-arm64-<sha256>.tar.gz` and `dist/checkpoint-worker-release.json`: separately checksummed optional checkpoint image and its matching runtime/source descriptor.
- Archive checksums, `dist/runner-release.json` and `dist/server-release.json`: fixed source/version, protocol and artifact identities. The published `release.json` also binds the registry digest.

Each runtime has its own file/link manifest and licenses. The Linux Runner includes its fixed LayerFS image. A Mac runtime built with checkpoint worker support includes its small controller and fixed artifact pin; the optional Linux ARM64 image is a separate download and is never loaded for native-local setup. Mac runtime packages contain no Linux addon, daemon, image or Rust compiler. Native dependencies stay pinned; only foreign-platform node-pty prebuilds are excluded and recorded. A portable Go archive writer normalizes tar metadata/order and keeps unchanged runtime content stable across installer rebuilds. Digests establish integrity, not a publisher signature; use a trusted release channel. Optional document parsing is Server-side and absent from Runner runtime packages.

Each native builder emits a schema-2 `runner-release-<platform>.json` contribution. All contributions must agree on version, source and protocol before the schema-2 `runner-release.json` combines their `runners` entries. The published `release.json` also uses schema 2. The Server embeds the complete catalog; use only the matching release catalog, preserving Runner identity and retained digest runtimes.

Runtime archives normalize file order, metadata and ownership. The trusted helper omits Git build metadata, so an unrelated UI/docs commit does not invalidate identical native runtime bytes. Code/dependency changes still change their file hashes; the release catalog retains the exact source commit and immutable artifact identity. Server and installer version/provenance remain independently bound to their release.

Runtime packaging precedes installer hashing; the Server then embeds that exact Runner descriptor. The final release catalog can hash the Server without a circular self-hash. A release build refuses dirty source. Unversioned `make package-daemon` creates local host-platform development packages; `make build-server-image` builds the development Server image. The `release` Docker target consumes the already verified Server bundle instead of rebuilding a different binary.

`server-release.json` and `release.json` list the exact supported archives in `servers`; `server` is the matching Linux artifact used by the container/native harness. Release verification rejects missing platforms, changed identities or checksums. The Linux builder cross-compiles Mac packages, then actual Apple Silicon and Intel Mac workers verify the relocated archives, embedded UI, authentication, versioned files and restart persistence before publication. Ordinary CI also builds each Mac Server and exercises discussion/file work in Mac WebKit. Those Server checks remain separate from actual Mac ARM64 Runner installation acceptance on macOS 15 and 26. The release gate runs the same immutable archives through relocated online/offline setup and the embedded Server Runner entrypoint, native DSH work, checks, publication and backup/restore, without host Node in the Runner PATH or paid model requests. Separate exact-source Mac boundary qualification and three-iteration native checkpoint measurements are retained inside the published acceptance report, including individual test skips and RSS; CI artifact expiry does not remove that release evidence.

Publication generates a versioned `camp.rb` Homebrew formula from verified archive digests. After public verification, update the maintained [tap formula](../Formula/camp.rb) and the fixed-version Go examples in [README](../README.md#get-started) and [installation](install.md#go-installation) together in a reviewed PR. The formula installs the complete Server and Git without enabling a service. Both Mac CI workers install/test its actual public archive and run that release's full Server lifecycle through Homebrew, verifying the test driver's source commit against the release catalog. This keeps future API changes independent of the previously published tap version. Main-branch CI also clones the public tap; PR CI uses its reviewed checkout. Tagged Go installation is separately checked on Linux and both Mac architectures after publication. On Apple Silicon it also performs public native Runner setup, foreground execution, repeated setup and launchd lifecycle through that installed Go entrypoint; the retained report must match the release version/source. Sanitized platform/package, Homebrew and public-Go reports are retained with the release.

Package mirrors are an explicit deployment operation; follow [package mirror staging](install.md#stage-a-package-mirror). Staging never changes an existing Runner.

`make smoke-bundle` tests the explicit Server mirror and offline paths. `node scripts/smoke-bundle.mjs github` and `github-offline` require published assets and test public distribution without Server staging. All modes relocate the installation, remove host Node from the Runner PATH, test repeat setup, native execution, configured checks, binary transfer, one publication, human approval/delivery, stopped backup/restore and cleanup with a deterministic provider. Local execution alone does not establish fresh-host portability or real-provider behavior. Use the [validation layers](../CONTRIBUTING.md#checks) and report which actually ran; Mac package acceptance also installs, starts, inspects, stops and removes its own launchd job, checking identity/journal retention and no model replay after reconnection. Login startup remains separate and untested by that harness. Linux systemd unit tests use a controlled substitute.

## Upgrade qualification

Release acceptance pins `v0.1.0-alpha.2` as the public upgrade baseline. `node scripts/smoke-upgrade.mjs` creates actual baseline Server data, then starts the current Server against it and verifies authentication/session, discussion, exact versioned file bytes, private drafts and history. This check supports Linux amd64 and both Mac Server architectures, needs Git, Go and public GitHub access, and performs no model call. Mac package verification also upgrades that baseline's data using the same immutable Server archives before publication.

On the supported privileged native host, `node scripts/smoke-bundle.mjs upgrade` first installs/runs the baseline Runner and completes a model-free source import. Explicit current-version setup must preserve the identity, credential, storage/registration IDs and exact stopped journal bytes; the upgraded Runner then completes native work, checks, human approval/delivery and backup/restore. The current runtime uses the staged Server mirror before publication. Public verification repeats this as `github-upgrade` using the newly published runtime. Both scenarios use a deterministic provider and record only sanitized evidence. They establish the named baseline, not arbitrary historical upgrades or real-provider success.

The named Runner upgrade targets Linux: `alpha.2` has no Mac Runner. Mac qualification instead checks repeated setup, foreground/background lifecycle and stopped backup/restore without replacing identity. Server data upgrades and Runner installation compatibility remain distinct guarantees.

## Release evidence

Attach a sanitized acceptance report to each release, or link retained CI artifacts from its release notes. Record the source commit and any local modifications, artifact digests, host OS/architecture, runtime pins, commands/scenarios actually run, and passes, failures, skips or untested scope. Report source, browser, fixture, native, fresh-host installation and real-provider results separately. Keep evidence retrievable for the supported lifetime of that release; a temporary CI link alone is insufficient.

Keep raw logs, screenshots and provider ledgers private in ignored directories. Publish only reviewed evidence without credentials or private team data. Maintained guides describe guarantees and reproducible procedures; release artifacts retain the dated results.

If a recheck uses a different test driver, record its source separately from the package source and preserve the original failed outcome. Report cached acquisition, powered-off VM startup and physical-host boot as distinct scopes. Resource reports distinguish assigned memory, sampled guest usage, process RSS, charged cache and disk allocation; missing/unsupported samples are not zero-cost successes.
