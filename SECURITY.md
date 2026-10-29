# Security

Camp currently serves one trusted team per Server. Review the [trust boundaries](docs/architecture.md#team-and-authority) and [deployment requirements](docs/install.md#supported-deployment) before exposing a deployment. Current implementation limits are recorded in [architecture](docs/architecture.md#current-implementation-limits).

## Report a vulnerability

Use **Report a vulnerability** in the repository's [Security tab](https://github.com/cofy-x/camp/security) when private reporting is available. If that option is unavailable, [open an issue](https://github.com/cofy-x/camp/issues/new) asking for a private reporting channel, without disclosing vulnerability details.

Privately include the affected commit or release, host platform, reproduction steps, observed impact and any relevant redacted output. Do not post exploit details, credentials, Runner connection codes, team files, journals or raw logs in public issues or pull requests.

Camp is under active development. Security fixes target `main`; there is no separate maintenance commitment for older releases. Public CI covers source, browser and fixture behavior; it does not establish isolation on every host or validate real-provider behavior.
