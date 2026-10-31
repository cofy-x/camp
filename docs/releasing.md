# Build, verify and publish Camp

This guide owns release assembly, publication gates and evidence. [Installation](install.md) owns operator setup, mirrors, upgrades and backup; [CONTRIBUTING](../CONTRIBUTING.md) owns development commands.

## Release pipeline

The [Release workflow](../.github/workflows/release.yml) is authoritative for jobs and artifact allowlists:

1. Bind a fixed, unused version to an exact reviewed `main` commit whose Source, Browser and Fixture CI passed.
2. Build/qualify the optional checkpoint image on native Linux ARM64. The Mac builder imports its fixed descriptor and builds the native Runner; it does not run that Linux worker.
3. Combine matching platform contributions on the privileged Linux builder. Build Linux packages and Server catalogs; verify native recovery, online/offline installation and the Server image.
4. Verify the exact Server archives on Apple Silicon/Intel Mac and native Runner packages on macOS 15/26 ARM64 before publication.
5. Publish only reviewed assets to GitHub Releases/GHCR. Pull requests do not publish releases.
6. On fresh hosts, verify anonymous public Linux/Mac installation, the named Linux Runner upgrade, tagged Go installation on all Server platforms and anonymous GHCR pull. Apple Silicon Go verification also exercises native Runner setup/execution and launchd lifecycle. Append actual distribution reports, including failures.

A prerelease whose public verification failed is not completed acceptance. GHCR package visibility is separate from repository visibility; ensure the package permits anonymous pulls using [GitHub's visibility controls](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

## Dispatch and rehearsal

Replace `vX.Y.Z` with a new, unused release tag. Dispatch publication only after the selected main commit's CI passes and publication is authorized:

```sh
VERSION=vX.Y.Z
gh workflow run release.yml --repo cofy-x/camp --ref main -f version="$VERSION"
```

A trusted development branch can rehearse the package pipeline:

```sh
VERSION=vX.Y.Z
gh workflow run release.yml --repo cofy-x/camp --ref your-branch \
  -f version="$VERSION" -f validate-only=true
```

Validation-only mode has read-only permissions, retains artifacts and creates no tag, Release or registry image. It grants neither main CI nor public distribution acceptance. Inspect its exact source and outcomes before proceeding. Failed or uncertain publication is reconciled against retained identities; never move a tag, replace asset bytes or relabel a private candidate as a public build.

For local packaging use [contribution commands](../CONTRIBUTING.md#build) and [worker tooling](../tools/layerfs-worker/README.md#source-builds-and-qualification). Unversioned `make package-daemon` creates host-platform development packages. `make build-server-image` builds a development image; the `release` Docker target consumes the verified Server bundle. Local builds do not publish or assemble another platform's native runtime by implication.

## Artifacts and provenance

| Artifact | Purpose |
| --- | --- |
| `camp-server-{linux-amd64,darwin-arm64,darwin-amd64}.tar.gz` | Complete Server, embedded Dashboard and licenses |
| `camp-runner-{linux-amd64,darwin-arm64}.tar.gz` | Small installer with fixed runtime identity |
| `camp-runtime-<platform>-<sha256>.tar.gz` | Digest-addressed runtime |
| `camp-runner-offline-<platform>.tar.gz` | Installer plus the identical runtime |
| `camp-layerfs-worker-linux-arm64-<sha256>.tar.gz` | Separate optional checkpoint image |
| `checkpoint-worker-release.json` | Worker/source descriptor |
| `runner-release.json`, `server-release.json`, `release.json`, checksums | Source, version, protocol, platform and artifact identities |

Each native builder emits a schema-2 `runner-release-<platform>.json`. Contributions must agree on source/version/protocol before combining `runners`; the published `release.json` also uses schema 2. Server embeds the complete catalog. Runtime packaging precedes installer hashing; the final catalog hashes Server afterward, avoiding a circular self-hash. `servers` lists supported Server artifacts; `server` selects the Linux container/harness artifact. Mismatched or missing platforms, identities and checksums fail validation.

Every runtime has a file/link manifest and licenses. Linux includes its fixed LayerFS image. A worker-enabled Mac runtime includes its controller/pin, with no Linux addon, daemon, image or Rust compiler; the optional image is downloaded separately and never loaded for native-local setup. Only foreign-platform node-pty prebuilds are excluded and recorded. Document readers remain Server-side.

Archives normalize file order/metadata/ownership, and the helper omits Git build metadata. Actual file hashes determine runtime identity; the catalog independently retains exact release source/version. Dirty source is refused. Checksums establish integrity through a trusted release channel, not a publisher signature.

After public verification, update the maintained [tap formula](../Formula/camp.rb) from the generated `camp.rb` asset and update the fixed-version examples in [README](../README.md#get-started) and [installation](install.md#go-installation) together in a reviewed PR. Mac CI checks the fixed public tap with its matching release-source driver, independently of later API changes. Homebrew installs Server/Git without enabling a service.

## Upgrade qualification

The [named baseline](../scripts/upgrade.mjs) is `v0.1.0-alpha.2`. `scripts/smoke-upgrade.mjs` creates baseline Server data and verifies authentication, discussion, exact file versions, private drafts and history on Linux and both Mac Server architectures. It needs Git/Go/public downloads and no model call.

`scripts/smoke-bundle.mjs upgrade` additionally checks Linux Runner setup against a staged mirror; `github-upgrade` repeats against public downloads. Preserve identity, credentials, storage/registration IDs and exact stopped journal bytes, then verify native work, checks, review/delivery and backup/restore. The baseline has no Mac Runner: Mac repeated setup and stopped backup/restore do not establish cross-version worker-image upgrade compatibility.

`make smoke-bundle` exercises the explicit mirror/offline paths; `github` and `github-offline` require public assets. These tests relocate packages, remove host Node from Runner PATH and use deterministic providers. Package launchd lifecycle is separate from login startup, which the harness does not test; Linux systemd unit tests use a controlled substitute. Follow [mirror staging](install.md#stage-a-package-mirror) without changing existing Runner installations.

## Release evidence

Retain sanitized evidence for the release's supported lifetime; temporary CI links alone are insufficient. Record source and test-driver modifications, artifact/runtime identities, actual host/engine, commands, outcomes, failures, skips and untested scope. Source, browser, fixture, native isolation, public installation and paid-provider results establish separate guarantees. A changed recheck driver does not change package provenance or replace an earlier failed outcome.

Keep raw logs, screenshots, credentials and provider ledgers private. Current guides contain contracts/procedures; fixed-version Release assets contain dated results. Report cached acquisition, powered-off VM startup and physical-host boot separately. Distinguish assigned memory, sampled guest usage, process RSS, charged cache and disk allocation; missing/unsupported samples are not zero-cost successes. Paid-provider checks require the [separate finite authorization](dsh.md#real-provider-verification).
