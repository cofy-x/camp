# Camp architecture

The [native Runner design](native-runner.md) owns the cross-platform execution boundaries and qualification requirements; the implementation described below remains the released Linux path until those checks establish another backend.

Camp has one trusted team's single Server, React desktop and independent Linux Runners. The [Server composition](../apps/server/internal/server/server.go) uses `remoteruns`; it never starts the legacy local Run scheduler or a DSH/LayerFS bridge. SQLite and immutable Git objects own shared results. LayerFS is disposable execution state.

Server owns identity/permissions, admission/scheduling, model proxy, durable files, review and notifications. Runner actively connects to Server and owns its journal, deduplication, local scheduling, lease watchdog, reconnection reconciliation and artifact upload/cleanup. Native executor prepares tools, calls DSH/LayerFS and checks/exports results without the business database. DSH owns the model/tool loop; LayerFS owns rebuildable execution workspaces. Even on one machine, Server and Runner are separate processes using the same remote protocol. Server has no Agent-execution dependency on host Docker, DSH or LayerFS.

The [root Go entrypoint](../main.go) and [release launcher](../internal/launcher/launcher.go) are distribution code, outside the Server's internal packages. The launcher installs the exact published Server or Runner archive and replaces itself with that executable, preserving process signals and exit status. It owns only a private, rebuildable version cache, never team configuration, credentials or Runner journals. This preserves the complete generated Dashboard and compiled-in runtime identities without checking generated bundles into source or rebuilding release metadata during `go install`. Standalone archives remain the offline path; source developers retain `make build`.

## Package boundaries

| Owner | Responsibility |
| --- | --- |
| [Platinum](../packages/platinum/src/types.ts) | App-independent windows, controls, icons, menus and keyboard/pointer behavior |
| [Dashboard app](../apps/dashboard/src/app/App.tsx) | Providers, startup, shared desktop data and application registry |
| [Features](../apps/dashboard/src/features) | Application definitions, views, scoped styles and focused tests |
| [Shared](../apps/dashboard/src/shared) | Generic HTTP/session and interaction helpers |
| [team](../apps/server/internal/team) / [configuration](../apps/server/internal/configuration) | Authentication and private versioned administrator settings |
| [collaboration](../apps/server/internal/collaboration) | Membership, explicit message admission and evidence preparation |
| [remoteruns](../apps/server/internal/remoteruns) | Admission, scheduling, model proxy, transport and publication |
| [runnerprotocol](../apps/server/internal/runnerprotocol) | Versioned, storage-independent machine wire types |
| [daemon](../apps/server/internal/daemon) | Outbound connection, durable inbox/outbox, authorization watchdog and retained uploads |
| [nativeexecutor](../apps/server/internal/nativeexecutor) | Pinned native execution, checks, export and cleanup; no storage/Room dependency |
| [Fixture executor](../apps/server/internal/daemon/fixture_tasks.go) | Deterministic scenarios using the same daemon protocol, journal and recovery lifecycle |
| [runs](../apps/server/internal/runs) | Shared bounded context preparation and local execution regression harness |
| [dsh](../apps/server/internal/dsh) / [execution](../apps/server/internal/execution) | Public DSH protocol/overlays, Bubblewrap boundary and process ownership |
| [layerfs](../apps/server/internal/layerfs) / [sandboxexec](../apps/server/internal/sandboxexec/README.md) | SDK bridge, Docker operations and trusted nonroot container helper |
| [runnertools](../apps/server/internal/runnertools) / [workspace](../apps/server/internal/workspace) | Run-scoped file/command capabilities and replaceable workspace lifecycle/capture contracts |
| [resources](../apps/server/internal/resources) / [library](../apps/server/internal/library) | Safe ingestion, immutable Git bytes and human publication using writer leases |
| [documents](../apps/server/internal/documents) / [documenttext](../apps/server/internal/documenttext) | Durable text-preparation queue and isolated bounded parsers |
| [storage](../apps/server/internal/storage) | Persistent business invariants and atomic state changes, including authority/version checks at commit |
| [httpapi](../apps/server/internal/httpapi) | Protocol parsing, request bounds, authentication integration and status mapping |

Features must not import the application registry or startup implementation. Keep file browsing, editing and publication in `features/library`; keep reusable resource transport with its API models and generic transport in `shared`. Platinum must not know Camp application IDs: use host callbacks for system actions and `AppDefinition.launchable` for launcher visibility. Removing an app should require no desktop-package changes. Add a layer only when it has a distinct owner. Validate transport, archive and execution inputs at their owning boundaries; storage rechecks the business authority and versions required for atomic publication.

Refactors preserve observable behavior; intentional product changes update callers, tests and contracts together. Keep React Hook dependencies complete and handle asynchronous failures. Fix lint findings without blanket suppressions.

## Team and authority

A Room holds team-visible discussion and Agent members; its primary Resource holds shared working files. A Thread is anchored to an existing Message. An Agent is a persistent identity and permission set. A Run is one explicit Agent request, with an AgentConversation scoped to Agent × Room/Thread. Personal editing drafts, Inbox, reads and layouts belong to the authenticated person. Collaboration task drafts are team-visible, but do not change shared Room files until final publication.

| Identity | Meaning |
| --- | --- |
| User | Team member and attributed actor |
| Agent | Persistent role, responsibilities and permissions |
| Runner | Registered execution machine with stable private storage |
| CollaborationTask | Goal, approved participants, finite grant, draft and progress |
| Run | One Agent execution, independently requested or one task round |
| Attempt | Actual execution authorization/lifecycle for that Run |
| SourceImport | Separate model-free source-capture operation |

Personal ownership does not create private Rooms or task results.

[Ownership and routing](../apps/server/internal/storage/ownership.go) distinguish invocation from management. Members create personal Agents and Runners. Owner/admin controls changes to owned objects and Agent sharing; enabled members can join team-owned Agents, while removing one requires an administrator. Automatic Room setup only shares team-owned defaults. Sharing an Agent into a Room lets teammates invoke it, without granting machine management or source-directory access.

Personal Agents keep their selected Runner. Team Agents use explicit Agent target, then Room route, then team default. A personal default is copied into new personal Agents, not consulted anew for each Run. Admission freezes the actual target and configuration; offline/busy Runners wait without reassignment. Permission revocation marks queued/active work and waiting tasks durably. Restoring permission cannot revive revoked work. Admission, model access and publication recheck authority.

## File and publication ownership

Uploaded originals are immutable and require an explicit working copy for edits. Runner imports retain an immutable initial commit and directly create a new working Resource. Later execution never writes back to the source directory. Source aliases are operator-approved, not browser-supplied host paths. Capture checks file identity and two manifests but is not an atomic filesystem snapshot; quiesce a source when that guarantee is required.

Ordinary writers acquire one durable, fenced Resource lease before choosing their latest baseline. Manual save and restore use the same lease and expected-revision compare-and-swap (CAS). Waiting writers consume no execution slot; read-only work can use stable snapshots concurrently. Server concurrency and Runner slots are configured independently; each Agent executes serially across Rooms. Defaults belong in [execution configuration](dsh.md#configuration-and-budgets).

A cleaned Sandbox export remains untrusted until Go validates entry types, paths, byte limits and digest. Git stores immutable bytes first. The final SQLite transaction validates Attempt, owner/fence, baseline and current permissions, then commits the Resource pointer, terminal Run, unique completion Message, publication, receipt and Inbox notification together. A failed final transaction cannot claim success. Idempotent upload/completion can finish persistence without a model replay.

Successful writing requires configured checks to pass. No checks is `not_configured`; human publications are `not_run`. Tool read-back or model claims are not independent checks. Failure/cancellation can retain confirmed captured files without publishing them. An unconfirmed export cannot be reconstructed from an unchanged host seed. Git/SQLite, not LayerFS checkpoints, are delivery authority. A crash between Git storage and SQL registration can leave unregistered objects; there is no general automatic resource/document retention collector.

## Collaboration tasks

[Task storage](../apps/server/internal/storage/tasks.go) and [remote orchestration](../apps/server/internal/remoteruns/tasks.go) own a persistent goal, approved Agent/Runner snapshots, finite allowance, inputs/questions and an immutable draft chain. One task has at most one active round. Waiting consumes no Runner slot, model capability or shared Resource lease.

Task rounds hold a task fence and advance only `checkpointCommit`. Handoffs and questions defer final checks. A writer finish validates checks and persists `publishing`, then briefly acquires the Resource lease and publishes only against `baseSharedCommit`. Accepted final checkpoints no longer need Runner connectivity. Shared edits during work leave the draft in `needs_attention`. Explicit continuation from latest files mounts the old draft as read-only evidence; no automatic merge or overwrite is allowed.

Execution starts consume rounds; each model request is reserved atomically before transmission. Unknown outcomes count and are not retried invisibly. Exhaustion requires another finite grant. Each round freezes its input revision; late inputs must be processed by another round or leave the task paused before finishing. Original goals and explicit inputs outlive bounded ordinary conversation history. Chat and task native sessions have separate scope keys.

Only task rounds expose structured handoff/question/finish tools. The private coordinator seals and drains workspace operations and journals a call-ID/body decision before acknowledging DSH. Intent alone cannot authorize recovery after process death; a complete retained result is required. Server alone schedules a successor and accepts publication. See [execution](dsh.md#collaboration-decisions).

## Remote execution and recovery

A Run is a request; its Attempt is the execution authorization. Connection epochs fence obsolete WSS connections without replacing Attempts or writer fences. WSS carries durable commands/events, ACKs, cancellation and lease grants. HTTPS carries immutable archives and the model proxy. No arbitrary port tunnel or shared Store is exposed. Machine credentials and browser sessions have separate boundaries; see the [API contract](api.md#daemon-protocol-v2).

Server persists commands before sending; daemon persists inbox before ACK and journal events/results before transmission. Identical replays return their original receipt; different content conflicts. This supports single publication, not exactly-once provider requests or external effects. Lease grants use the causal request's monotonic send time. Expiry stops execution; delayed grants cannot extend authorization from a newer heartbeat.

Brief WSS loss can preserve an Attempt while an independent model stream continues within its lease. Server restart interrupts a broken in-flight model stream without resending it. Daemon restart cleans incomplete execution and can upload a fully journaled result. Model access ends before the bounded upload phase. Cancellation persists intent and revokes model access first. Confirmed cleanup is Cancelled; uncertain cleanup is Interrupted and suspends the Runner in the same final transaction. Disabled Runners can retrieve terminal receipts; revoked credentials lose machine access.

[Native session eligibility](../apps/server/internal/nativeexecutor/executor.go) requires completed native work, acknowledged shutdown, normal exit and accepted Camp completion, plus matching Runner/storage/runtime/generation. Cancellation, missing state or uncertain exit invalidates resume. The next explicit request rebuilds bounded visible context and announces it; an uncertain prompt is never replayed automatically. File-version changes are announced without necessarily resetting context; Agent/resource/configuration changes can require a generation.

Server claims its ownership fence before migration/recovery. Each daemon owns its journal and bridge lock; startup resets only its private runtime home. Other Runners remain untouched. Server shutdown closes SSE/control streams and model streams, then drains HTTP and closes SQLite. Daemons enforce their own leases.

## Human review and delivery

[Review storage](../apps/server/internal/storage/reviews.go) owns actor permissions, optimistic revisions and transitions. HTTP checks immutable file existence before admission; the transaction rechecks the Resource pointer. A review pins one path and commit, names a requester/reviewer and creates one discussion Thread. Decisions, audit events, Messages, receipts and Inbox entries commit atomically and create no Runs. Execution success does not approve a document.

Only the enabled assigned reviewer approves or requests changes. Requester/admin resubmits, reassigns, cancels or delivers. Changing any shared file makes an unfinished review outdated because approval covers the complete Resource revision. Reassignment clears a decision; delivery requires current approved bytes. Delivered and cancelled reviews are terminal. Later file changes cannot alter delivery.

## Desktop state

The [registry](../apps/dashboard/src/app/registry.ts) composes five product apps, an About window and a development-only Gallery. Library is the sole file browser and text editor; Appearance is inside Settings. All apps share one React tree. The [team provider](../apps/dashboard/src/team/TeamContext.tsx) owns shared entities, reconnectable events, Inbox and unread state even while Workspace is closed. Typed navigation opens the exact destination in an existing window after draft-safe navigation. `lastMessageSeq` is a read position; `activitySeq` is an event cursor.

[Private drafts](../apps/dashboard/src/team/usePrivateDraft.ts) combine server CAS revisions with account-scoped local recovery. Closing, switching and sign-out flush drafts first; errors retain local content. A lost send response retains the original request body/ID. Delayed responses cannot mark newer text published or replace a newer destination. Hide/shade keeps app state mounted; pointer previews remain separate from committed layouts and pending saves retain ordering on failure.

Library freezes resource/path/baseline for an editor or review request. Obsolete reads cannot install another file after navigation. Existing filenames open saved bytes. Tabs preserve reading state; listing failures end loading and offer retry. Environment forms keep editing baselines separate from observed settings and save receipts. That API has no atomic revision precondition, so the UI cannot prevent an unseen concurrent administrator update; see [the open concurrency work](#resource-configuration-concurrency).

## Readiness reporting

[Remote readiness](../apps/server/internal/remoteruns/manager.go) and [GettingReady](../apps/dashboard/src/features/settings/GettingReady.tsx) report configuration, prior provider responses, connected Runner self-test reports and recorded native task success separately. Refresh is a read-only GET, with its own timestamp; it preserves observation timestamps and makes no model call, dependency probe or admission change. Failed/obsolete refreshes retain the last accepted report without claiming fresh success.

Execution self-tests belong to explicit Runner setup, which owns the journal lock and confirms isolation/export/cleanup without a model request. Optional readers belong to document preparation and do not gate discussion/plain-text work. A future active probe needs an explicit owner, scope and protocol; a refresh cannot stand in for one. Readiness is an aggregate, not a guarantee of a particular Agent's routing, grants or future task outcome.

## Storage

[Numbered migrations](../apps/server/internal/storage/migrations) are the Goose schema; [named queries](../apps/server/internal/storage/queries) generate private sqlc rows. Business models/errors remain handwritten. Run `make generate` and `make generate-check` together with SQL changes and maintain generated Go source.

The current Team baseline upgrades through append-only migrations, retaining Rooms, versions, reviews and historical identity. Old Agent/Runner objects remain team-owned and historical local Runs get no synthetic Runner. Discarded pre-Team schemas fail with an explicit request for a new directory; they are not reset or backfilled automatically. A broken migration ledger is an error, not a clearing instruction. API compatibility for discarded development versions is unnecessary; existing team data still requires safe migration and a stopped backup.

Back up the entire stopped Server data directory, including Git and private model revisions. Runner identity, journal and native sessions have their own backup lifetime. [Installation](install.md#data-upgrades-and-backup) owns deployment, release integrity and restore procedure; [execution](dsh.md) owns native isolation and configuration. Source checks cannot establish native or real-model guarantees.

## Current implementation limits

The boundaries below describe current implementation choices, not permanent requirements for a particular schema, host platform or storage layout. Preserve the stated user guarantees when changing them. Open defects have separate work items below, with an owning module and observable completion conditions. Link an issue when one exists; remove resolved work items after updating the contract and its tests. Significant design rationale belongs with the owning contract.

| Boundary | Current implementation and guarantee to preserve |
| --- | --- |
| Source preservation | [FinishSourceImport](../apps/server/internal/storage/remote_sources.go) creates a working Resource/Room and retains its initial immutable Git commit in SourceImport. A second immutable-original Resource row is not required by this guarantee. Later edits preserve the captured source version and never modify the host directory. Capture's manifest checks are not an atomic filesystem snapshot. |
| Browsers select Runner source aliases | Runner import enforces this. A separate [Server import](../apps/server/internal/httpapi/rooms.go) still accepts an administrator-supplied host path through an operator allowlist; it is not unrestricted Runner browsing. |
| Prepared execution and checks | Production copies dependencies into the LayerFS seed and runs checks there. Read-only dependency mounts and a separate disposable verification copy describe the local regression harness, not production; see [project runtime](../tools/project-runtime/README.md). Preserve execution isolation and accurate check provenance when changing this layout. |
| Upgrades preserve team data | Append-only Team migrations are implemented. [Migration validation](../apps/server/internal/storage/migrations.go) explicitly rejects discarded pre-Team baselines; there is no universal historical-data upgrader or automatic reset. Restore is the stopped manual procedure in [installation](install.md#data-upgrades-and-backup). |

Runner check/output reports assume a trusted machine; they are not remote attestation against a hostile operator. Source/browser/fixture results alone do not establish native isolation, fresh-host portability or real-provider success.

### Resource configuration concurrency

Open work; owners: `storage`, `httpapi` and Library. The [Resource PATCH API](api.md#library-and-publication) has no revision precondition. Two administrators can overwrite each other's environment changes despite the UI retaining its editing baseline.

Completion requires an atomic revision precondition, a conflict response for stale updates, and a UI recovery path that preserves unsaved edits. Verify two administrators saving from the same revision: only one update commits, and the other can inspect the latest configuration before explicitly retrying. Update the API contract and callers together.
