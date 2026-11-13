# Response and storage performance

This contract applies when adding UI reads, live events, histories, rendering or streamed writes. It records the constraints behind the existing performance work and the checks to extend with a feature. It is not a latency guarantee or a dated benchmark report.

[Architecture](architecture.md) owns state, transactions and module boundaries. [API](api.md) owns wire formats, cursors and exact limits. [Design](design.md) owns visible interactions, attention cost and visual review. Keep those details with their owner rather than copying them here.

## Primary content and bounded work

- Let the conversation, matter state, result or file open independently of optional members, configuration, history and diagnostics. Give dependent panels their own loading, errors and retry. A permission-dependent action still waits for matching current context; fast reading must not invent an execution default.
- Read a small projection for a list or current status. Load file bodies, frozen requests, complete execution records and raw logs only for the view that needs them. Do not fetch a full object and discard most of it in the browser.
- Bound history by both records and encoded bytes. Filter and seek in storage before paging; do not fetch all rows and slice them or aggregate every Room in the browser. Direct message references seek to the target without walking intervening pages.
- Retain one selected display window. Earlier/Later replaces that window, a historical frontier stays frozen and Latest explicitly resumes current observations. Never drain earlier pages in the background to populate a normal view. API limits and fixture coverage live in the linked contracts and tests below.
- Keep input updates local to the composer/form, with stable message and reading controls. Reuse unchanged parsed Markdown; parse long reports when opened. Preserve focus, selection, reading anchors and drafts during updates. Optimize additional rendering or virtualization only for measured work remaining after reads and mounted history are bounded.

When replacing an ordinary-view read path, remove its superseded callers and refresh loops. Full or compatibility readers need an explicit remaining owner; normal navigation must not fetch both the old heavy record and its new projection.

## Reads and live observations

Use the account-scoped shared read store for accepted GET responses and in-flight deduplication. The path and query identify the read scope. A transient failure retains that scope's accepted content with a local retry; revoked access clears it. An old Room, file or page response cannot replace a newer navigation choice.

Complete a slow active read, then perform a needed follow-up when notifications arrive. Do not create a cancel/restart loop for each event. Suspend read subscriptions for shaded windows and hidden documents without discarding their mounted interaction state; revalidate on return. Optional apps load inside their existing window and recover a failed chunk locally.

Keep browser queries/commands on HTTP and observations on the shared reconnectable SSE subscription. Capture the startup frontier before domain reads; reconnect from the durable event cursor. Commit notifications before waking readers, and retain durable replay/repair independently of process-local wakeups. Runner control and model traffic retain their separate [execution protocol](architecture.md#remote-execution-and-recovery).

Batch affected identities and invalidate matching reads. A new event or view must update the shared invalidation map and its focused coverage. Pure output activity must not refetch Room conversations, task grants or current business-state summaries. Broad team changes and reconnect/foreground recovery can revalidate more widely; a healthy idle desktop must not periodically reload every open app. Health checks and stream heartbeats are separate from business-list refreshes.

Writes, private-draft CAS, uncertain-request receipts and explicit verification reads remain outside the shared response cache. Cached presentation is never a new permission or decision baseline. Preserve the current review, publication and execution preconditions in their owning controllers and Server transactions.

## Streamed writes and storage

Keep frequent output appends on the small progress/chunk projection, separate from frozen execution requests, grants and pending commands. Compose the existing full read contract only on demand. A streamed chunk must not materialize or rewrite a whole historical Run or Task.

Retain original event bodies, sequence/hash receipts, immutable file versions and restart recovery. A display tail or summary is not an audit replacement. Separate output-activity notifications from actual business-state transitions.

Keep SQLite transaction callbacks short. Git, filesystem preparation, network and model work happen outside business transactions, with current authority and frozen versions rechecked at acceptance. Preserve the [storage configuration and snapshot rules](architecture.md#package-boundaries); increasing connections, weakening durability or replaying a transaction callback is not a default response to a slow UI.

Measure contention and write amplification before changing the storage engine, database concurrency, worker count or browser transport. Such changes need a measured bottleneck, an owning contract and equivalent authorization/recovery coverage. A faster transport cannot remove oversized reads, unnecessary writes or repeated rendering.

## Source and regression map

These are current entry points, not a second list of implementation constants. Read the affected source and extend its focused regression cases.

| Area | Implementation | Regression evidence |
| --- | --- | --- |
| Shared reads, invalidation and visibility | [Read store](../apps/dashboard/src/shared/reads/store.ts), [invalidation](../apps/dashboard/src/shared/reads/invalidation.ts), [visibility hook](../apps/dashboard/src/shared/reads/useRead.tsx), [team observations](../apps/dashboard/src/team/TeamContext.tsx) | [Read-store cases](../apps/dashboard/src/shared/reads/store.test.ts), [live desktop cases](../tests/scoped-live-reads.spec.ts) |
| Bounded conversations and related execution metadata | [Conversation queries](../apps/server/internal/storage/queries/conversation_pages.sql), [activity projection](../apps/server/internal/storage/conversation_activity.go) | [Large-history/storage cases](../apps/server/internal/storage/conversation_pages_test.go), [Room loading/navigation cases](../tests/room-loading.spec.ts) |
| Status, reply, files, activity and diagnostics separation | [Run inspector](../apps/dashboard/src/features/workspace/useRunInspector.ts), [activity projection](../apps/server/internal/storage/run_activity.go) | [Inspector cases](../tests/execution-inspector-performance.spec.ts), [activity storage cases](../apps/server/internal/storage/run_activity_test.go) |
| Streaming ingestion and retained originals | [Output projection](../apps/server/internal/storage/run_output.go), [output queries](../apps/server/internal/storage/queries/run_output.sql) | [Output cases](../apps/server/internal/storage/run_output_test.go), [migration/restart cases](../apps/server/internal/storage/run_output_migration_test.go), [opt-in contention measurement](../apps/server/internal/storage/run_stream_qualification_test.go) |
| Notification frontier and post-commit wakeup | [Bootstrap](../apps/server/internal/storage/settings.go), [durable events](../apps/server/internal/storage/rooms.go), [post-commit wakeup](../apps/server/internal/storage/store.go) | [Atomic message/cursor/wakeup cases](../apps/server/internal/storage/workspace_updates_test.go), [live desktop cases](../tests/scoped-live-reads.spec.ts) |
| File reading and optional workflows | [Library controller](../apps/dashboard/src/features/library/useLibraryController.ts), [bounded preview](../apps/dashboard/src/features/workspace/readFilePreview.ts) | [Library reading cases](../tests/library-reading.spec.ts), [preview bounds](../apps/dashboard/src/features/workspace/readFilePreview.test.ts) |
| Scoped human review history | [Review list storage](../apps/server/internal/storage/review_list.go), [seek queries](../apps/server/internal/storage/queries/review_list.sql), [list navigation](../apps/dashboard/src/features/reviews/ReviewList.tsx) | [Large-history/authority/migration cases](../apps/server/internal/storage/review_list_test.go), [HTTP bounds](../apps/server/internal/httpapi/review_list_test.go), [browser paging/recovery](../tests/review-history.spec.ts) |

Needs me remains a Server-owned [attention projection](architecture.md#personal-attention); reading Activity never resolves it. Native checkpoint costs belong to the [checkpoint contract](layerfs-worker.md), and CI/package cost and cache validity belong to [contribution checks](../CONTRIBUTING.md#checks) and [release verification](releasing.md). None is established by browser fixtures alone.

## Validation and evidence

Start from the actual slow journey: cold desktop, Room switch, completed Work, file preview, activity history or a live stream. Record the source revision, build, browser/host, data size, connection conditions, cache state and selected window/page. Compare before/after with the same scenario; a sleep or screenshot inside a script is not application latency.

Inspect requests and encoded bytes as well as perceived delay. For the affected path, measure input-to-visible-content time, long tasks/rendering, reads per navigation/event, cancellation/restart behavior and idle requests. For streamed storage, also inspect transaction/read latency, connection waits and WAL/write cost. Use the existing bounded-history fixtures and opt-in streaming probe where relevant; synthetic local measurements do not establish remote, native or real-provider latency.

Exercise only the affected boundaries, including slow/failed related reads, obsolete responses, notification bursts, multiple windows, shade/resume and frozen history. Confirm primary content stays usable, drafts and focus survive, accepted errors have recovery, and unknown data is not shown as an empty/zero state. Preserve authority, immutable originals and uncertain-write behavior while improving speed.

Run affected checks first through [CONTRIBUTING](../CONTRIBUTING.md#checks). Use deterministic request, projection, page-size and recovery assertions for regression; add or change timing gates only with reproducible host-specific evidence. Record actual executed/skipped checks, fixture/protocol/native/provider scope and matching inputs for any reused result. A previous pass or a source-only check is not new performance evidence.

Keep traces and benchmarks as scoped evidence, not permanent universal timing claims. Put unresolved performance work, its owner and completion condition in an issue and the owning [implementation limits](architecture.md#current-implementation-limits). Update this source map when owners move and remove superseded rules. Follow the [UI checklist](design.md#review-checklist) for visible changes and the [private-data policy](../AGENTS.md) for any real-data observation.
