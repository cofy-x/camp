# Camp

Camp is a self-hosted workspace for trusted teams and AI agents, with a Platinum-style browser desktop. One Server stores the team's conversations and files; members join in a browser, and connected Runners execute Agent work.

- Discuss work in Rooms and Threads.
- Give Agents tasks or collaborate with multiple Agents, with explicit access and execution limits.
- Keep original sources, versioned shared files and private editing drafts.
- Review work with people and deliver an exact saved version.

Camp is in alpha and under active development.

## Get started

With Go and Git installed:

```sh
go install github.com/cofy-x/camp@v0.1.0-alpha.3
camp
```

Ensure Go's binary installation directory is on PATH. The CLI downloads and verifies the matching complete Server on first use; no source checkout or Node setup is required.

On macOS, Homebrew installs the Server and Git:

```sh
brew tap cofy-x/camp https://github.com/cofy-x/camp
brew install cofy-x/camp/camp
camp
```

Open the printed browser address and create your team using the token from the private file named in the terminal.

To enable Agent work, configure a model in Settings and follow **Runners → Connect Runner**; `camp runner setup` walks the machine operator through connection. Teammates only need the browser address and an invitation.

See the [installation guide](docs/install.md) for standalone archives, Docker deployment, Runner prerequisites, upgrades and backup. Choose a fixed version from [Releases](https://github.com/cofy-x/camp/releases).

## Platforms

| Component | Supported hosts                             |
| --------- | ------------------------------------------- |
| Server    | Linux amd64; macOS Apple Silicon and Intel  |
| Runner    | Ubuntu 24.04 amd64; macOS 15+ Apple Silicon |

Mac Runners use native tools, Seatbelt and a local workspace. Linux Runners use LayerFS. The [deployment guide](docs/install.md#supported-deployment) lists host requirements and platform-specific capabilities.

## Documentation and contributing

- [Team workflow](docs/workflow.md) — discussion, files, Agent work, collaboration and human review.
- [Contributing](CONTRIBUTING.md) — local development, builds, checks and pull requests.
- [Security](SECURITY.md) — trust boundaries and vulnerability reporting.

Bug reports and focused proposals are welcome in [Issues](https://github.com/cofy-x/camp/issues). Coding agents should also read [AGENTS.md](AGENTS.md).

## License

Camp is licensed under the [MIT License](LICENSE). Bundled dependencies and [typefaces](packages/platinum/src/fonts/README.md) retain their own licenses and notices.
