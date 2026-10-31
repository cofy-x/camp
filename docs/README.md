# Camp documentation

Start with the guide for the work you are doing. Each contract has one owner; related guides link to it instead of keeping another copy of its rules.

## Use and operate Camp

| Guide | Owns |
| --- | --- |
| [Product overview](../README.md) | Product scope and quick start |
| [Team workflow](workflow.md) | Discussion, sources, Agent work, shared files, review and delivery |
| [Installation](install.md) | Supported hosts, fixed-version setup, services, package mirrors, upgrades and backup |
| [Security](../SECURITY.md) | Trust model and private vulnerability reporting |

## Develop and maintain Camp

Read [AGENTS.md](../AGENTS.md) and [CONTRIBUTING.md](../CONTRIBUTING.md), then the contract for the area being changed.

| Contract | Owns |
| --- | --- |
| [Architecture](architecture.md) | Module/state ownership, durable authority and current implementation limits |
| [API](api.md) | Authentication, wire types, request bounds and transactional preconditions |
| [Design](design.md) | Product language, desktop interactions and visual review |
| [Execution](dsh.md) | DSH integration, model configuration/budgets and explicit provider acceptance |
| [Native Runner](native-runner.md) | Workspace, sandbox, process ownership and native platform acceptance |
| [Mac checkpoint worker](layerfs-worker.md) | Complete-tree capture, transport, receipts, reconstruction and backend choice |
| [Releasing](releasing.md) | Package assembly, reviewed-main publication, distribution checks and evidence retention |

Tool-specific commands and protocols live beside their implementation: [DSH](../tools/dsh-runtime/README.md), [Camp tools](../tools/dsh-runtime/camp-tools/README.md), [Linux LayerFS](../tools/layerfs-runtime/README.md), [Mac worker](../tools/layerfs-worker/README.md), [project preparation](../tools/project-runtime/README.md), [document readers](../tools/document-runtime/README.md), [Seatbelt qualification](../tools/seatbelt-runtime/README.md), [sandbox helper](../apps/server/internal/sandboxexec/README.md) and [remote validation](../tools/remote-dev/README.md).

[Documentation maintenance](../CONTRIBUTING.md#documentation) owns writing and deduplication rules. Track unresolved behavior in [implementation limits](architecture.md#current-implementation-limits); dated host/package/provider outcomes belong in [release evidence](releasing.md#release-evidence).
