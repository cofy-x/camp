# GitHub observations

A Room can receive GitHub repository webhooks in observation mode. Deliveries create immutable events awaiting a teammate's decision. Reading, refreshing, configuring a source and receiving a webhook make no model request. A teammate chooses a goal and approved Room Agents in **Start collaboration…** before work starts. The [task authority and publication contract](architecture.md#collaboration-tasks) remains unchanged.

## Connect a repository

1. As an administrator, open the Room's **GitHub** panel, then **Sources**.
2. Add `owner/repository` and a random webhook secret of 32–256 bytes. Events and original payloads will be visible to every enabled team member, like the Room.
3. Open **Webhook setup** and copy the Payload URL into that repository's GitHub Webhooks settings. Select `application/json`, enter the same secret, retain SSL verification and subscribe to **Issues**, **Pull requests** and **Issue comments**.
4. Use an externally reachable HTTPS Camp origin. Loopback HTTP is supported only for local protocol checks; this feature does not create a tunnel or configure GitHub, DNS, certificates or background services.

Camp verifies `X-Hub-Signature-256` against the exact body using HMAC-SHA256 and a constant-time comparison, following [GitHub's signature contract](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries). This endpoint accepts no member Cookie, browser Origin or Authorization header. The ordinary team APIs retain session and Origin protection. The [API contract](api.md#github-observation) owns request and response details.

Sources are fixed to a Room and repository name. The first valid delivery pins the repository's numeric identity; later deliveries must match both. A rename, transfer or replacement needs a new explicitly configured source. Disable the old source to retain its events and decisions. The same address can then have a replacement source, including after repository deletion/recreation. Only one source for that Room/address can be enabled at a time; re-enabling an older source requires disabling its replacement first. Repository IDs distinguish their histories. A Room supports up to 32 retained sources.

Clearing **Enabled** stops new deliveries and confirmations, including pending events. It preserves observations, receipts and already authorized tasks; use a task's own cancellation controls to stop that work. Rotating a secret immediately rejects deliveries verified with the old credential, including an ingress request that raced the rotation. Update the repository's webhook secret separately.

## Decide an event

The initial view lists events awaiting confirmation. **All events** includes accepted, dismissed and ignored observations. Opening an event shows its subject, external actor, original text and next action. **Event details** holds transport metadata and the original JSON download. GitHub identities are never mapped to Camp members. Bot, unresolved/ghost and unsupported events remain recorded as ignored observations.

The supported candidate actions are:

| Event | Actions |
| --- | --- |
| `issues` | `opened`, `reopened`, `edited` |
| `pull_request` | `opened`, `reopened`, `edited`, `synchronize`, `ready_for_review`, `closed` |
| `issue_comment` | `created`, `edited`, including PR issue comments |

Other event/action combinations and connection pings are recorded as ignored. This implementation does not poll GitHub history or automatically recover missed deliveries. Inspect GitHub's delivery history and explicitly redeliver a missed delivery, as described in [GitHub's delivery guidance](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks). No GitHub API token, repository checkout or external reply permission is granted.

**Start collaboration…** prepares an editable goal. It starts nothing until the teammate selects participants and submits the existing finite task authorization. The complete normalized event is copied into the task's append-only input log in UTF-8 chunks, labelled as untrusted external context supplied by the confirming member. Chunk wrappers preserve the authored body's whitespace. The task freezes the accepted input frontier and records its source event ID. Source context uses input protocol 1 and summary responses so large originals remain page-readable. The original webhook bytes are retained independently, without backfilling older tasks or changing their request hashes.

Confirmation writes the event decision, task, inputs, first queued Run, admission journal and task receipt in one SQLite transaction. One event can have one final human decision. Concurrent confirmations cannot create a second task, and an exact request replay returns its original task even after the source, model or route changes. Dismissal records the member, time and required reason without starting work. Event text cannot select Agents, expand budgets or resume work.

## Delivery, recovery and private state

The Server acknowledges a webhook only after its observation and delivery receipt commit. The delivery GUID is unique within its source; changed content under the same GUID conflicts. Byte-identical redeliveries are also deduplicated when their GUID changes. Ordering uses committed local sequences, rather than external timestamps. Reads use bounded pages and a frozen event frontier; decision states on those immutable events remain current.

After an uncertain task start, retain its exact request body and ID and use **Retry Start**, or open the event's recorded collaboration. Configuration and dismissal retain a private per-user receipt. **Check Saved Operation** reads that receipt without sending another write. Browser recovery stores only the operation ID for source settings; it never stores a webhook secret. Clearing an unresolved configuration recovery does not cancel an in-flight write. Expected revisions, source uniqueness and immutable event decisions still protect subsequent writes.

Raw deliveries are limited to 1 MiB, candidate titles to 1024 UTF-8 bytes and bodies to 64 KiB. Oversized/malformed deliveries fail explicitly; originals are never silently truncated. Credentials live in owner-only immutable files under `configuration/github-webhooks` in Server's data directory. SQLite stores opaque references and request hashes, never secret text. A file persisted before a failed database write can remain unreferenced; it cannot activate a source and is not automatically purged. Back up the complete stopped Server directory as described in [installation](install.md#data-upgrades-and-backup).

This is an observed-event and human-confirmation workflow. General Thread subscriptions, model assessment, automatic admission, Feishu and external reply effects remain [implementation limits](architecture.md#current-implementation-limits).
