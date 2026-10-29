# Contributing to Camp

Follow the [cofy-x contribution guidelines](https://github.com/cofy-x/.github/blob/main/CONTRIBUTING.md) and use [GitHub issues](https://github.com/cofy-x/camp/issues) for reproducible bugs and focused proposals. Read [SECURITY.md](SECURITY.md) before reporting a possible vulnerability. Camp is under active development; discuss changes to execution authorization, persistence, public APIs or deployment boundaries before building a large change.

## Development

Fork the repository and create a branch for your change. Follow [AGENTS.md](AGENTS.md) for development constraints and the owning contracts. [README](README.md#local-development) owns toolchain versions and setup; its [checks table](README.md#checks-and-maintenance) owns test prerequisites and commands. [Installation](docs/install.md#supported-deployment) owns deployment support. A source checkout can run the Server without native Runner dependencies or model credentials.

Keep changes focused, preserve existing data, and update the owning documentation when behavior changes. Add tests for meaningful behavior and failure cases. Keep secrets, personal team data and generated runtime files out of patches and issue attachments. Shared dependency checkouts are references; integrate through public interfaces and pinned dependencies.

## Pull requests

Explain the problem, resulting behavior and relevant tradeoffs. Include the commands you actually ran and their results, including skipped cases. UI changes should include reviewed images using synthetic data and cover the interactions described in the [design review checklist](docs/design.md#review-checklist).

The [CI workflow](.github/workflows/ci.yml) runs these checks on GitHub-hosted Linux workers:

| Job | Scope |
| --- | --- |
| Source | Formatting, types, lint, documentation links, generated SQL, Go race tests, script/frontend unit tests, production build and smoke |
| Browser | Production UI with fresh keyless data in Chromium, Firefox, WebKit and touch projects |
| Fixture | TLS/Compose with two fixture Runners, collaboration, publication, cancellation, restart and reconnect |
| Server / darwin-arm64 and darwin-amd64 | Actual Mac builds, launcher race tests, production authentication/file/restart smoke and Mac WebKit discussion/file checks |

Run the checks relevant to your change before opening a pull request. CI uses a read-only token without model credentials, uploads no raw reports, and publishes no releases. Native DSH/LayerFS, privileged host cleanup, packaged installation and real-provider acceptance remain separate checks under [execution](docs/dsh.md#real-provider-verification) and [release evidence](docs/install.md#release-evidence). A skipped native case is not native validation.

Third-party code and assets retain their own licenses and notices. Preserve those notices when changing dependencies or distribution contents; see [license information](README.md#license).
