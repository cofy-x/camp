# Camp API

Web/API share one origin; Vite proxies `/api` in development. Responses are JSON unless downloading bytes or streaming events. Errors are `{error,code}`; clients must check HTTP status before marking writes saved. IDs are opaque. File selectors use safe relative paths and retained exact commits, never Git expressions.

Route handlers in [httpapi](../apps/server/internal/httpapi) and machine handlers in [remoteruns/http.go](../apps/server/internal/remoteruns/http.go) define wire parsing; [storage types](../apps/server/internal/storage/room_types.go) and [runnerprotocol](../apps/server/internal/runnerprotocol/protocol.go) define models. This document owns externally meaningful permissions, replay and version semantics.

## Boundary and authentication

Browser API, SSE and downloads require a revocable `camp_session`, except health, auth/status, setup, login and invite acceptance. The HttpOnly, SameSite=Strict cookie is Secure for HTTPS. Unsafe requests require an exact allowed Origin. JSON routes accept one UTF-8 object with `Content-Type: application/json`; unknown fields and top-level null fail. Uploads use bounded multipart. Host validation also covers UI assets. Forwarded headers do not redefine trust; there is no wildcard CORS. `CAMP_PUBLIC_ORIGIN` declares the browser origin.

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/health` | Database/service liveness, not execution readiness |
| GET | `/api/auth/status` | `{setupRequired,user,team}` |
| POST | `/api/auth/setup` | `{token,username,name,password,teamName?}` → `{user}` and cookie |
| POST | `/api/auth/login` | `{username,password}` → `{user}` and cookie |
| POST | `/api/auth/accept-invite` | `{token,username,name,password}` → `{user}` and cookie |
| POST | `/api/auth/logout` | `{}`; revoke this session, leave work running |
| GET / PATCH | `/api/team` | Read metadata; admin PATCH `{name}` |
| GET | `/api/team/members` | `{members}` |
| PATCH | `/api/team/members/:id` | Admin `{enabled?,role?}`; preserve last enabled administrator |
| GET / POST | `/api/team/invites` | Admin list/create `{expiresInHours?}`; token returned once |
| DELETE | `/api/team/invites/:id` | Admin revoke |

Setup uses a private one-time file credential. Passwords use Argon2id; session and invitation secrets are stored hashed, never returned in metadata. Account disable revokes sessions/SSE and pending delegated work while preserving authorship. Authentication limits return 429 with Retry-After. All Rooms, tasks and shared files remain team-visible; ownership is management authority, not private Room access.

### Identity avatars

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/avatars` | Authenticated bundled catalog `{avatars:[{id,name,kind}]}`; kind is `member` or `agent` |
| GET | `/api/avatars/:id` | Authenticated, immutable SVG artwork for one catalog ID |
| PATCH | `/api/me/avatar` | Own profile only; `{avatar,expectedAvatar}` → `{user}` |

Users and Agents expose a persisted `avatar` catalog ID. Creation assigns a random least-used default of the appropriate kind. Explicit selections may repeat. Member changes compare `expectedAvatar`; a stale selection returns 409 `profile_conflict`, while a retry whose desired avatar is already current returns that profile. Authority, state and the team event commit together. Agent changes use the existing owner/admin check and Agent revision. Cross-kind and unknown IDs return 400 `invalid_avatar`; arbitrary image URLs and uploads are unsupported.

The append-only schema upgrade transactionally assigns missing avatars before serving, preserving identities, timestamps, Agent revisions and frozen execution records. Historical snapshots may lack the field. Avatars are presentation metadata; message author IDs/names and admitted execution inputs remain authoritative.

## Rooms, Agents and messages

| Method | Path | Contract |
| --- | --- | --- |
| GET / POST | `/api/rooms` | `{rooms}`; create `{name,description}` with empty working Resource |
| PATCH | `/api/rooms/:id` | Metadata, `resourceId` and joined `defaultAgentId` |
| GET / POST | `/api/agents` | `{agents}`; member creates owned identity |
| PATCH | `/api/agents/:id` | Owner/admin update including current `revision` |
| GET | `/api/rooms/:id/members` | `{members}` |
| PUT | `/api/rooms/:id/members/:agentId` | `{}`; member may join enabled team Agent, owned Agent requires owner/admin |
| DELETE | `/api/rooms/:id/members/:agentId` | Owner/admin leave; cancel this membership's work |
| POST | `/api/rooms/:id/agents` | Create owned identity and membership atomically; no execution |
| GET | `/api/rooms/:id/messages?after=seq&threadRootId=...` | `{messages}` |
| GET | `/api/rooms/:id/messages/:messageId` | `{message}` |
| POST | `/api/rooms/:id/messages` | Send input → `{message,runs,replayed}` |

```json
{
  "requestId": "stable-client-request-id",
  "content": "Please draft the proposal.",
  "threadRootId": "",
  "recipientAgentIds": ["writer-id"],
  "references": [
    {
      "kind": "file",
      "resourceId": "...",
      "commit": "exact-40-char-revision",
      "path": "brief.md"
    }
  ],
  "postOnly": false
}
```

Selected plus mentioned joined Agents produce at most one Run each, with at most 16 distinct recipients including mentions. With neither, a message is discussion. `postOnly:true` suppresses Agent mentions and cannot accompany explicit recipients. Human mentions notify people. Agent/system output is not an admission source. Joining never replays history. Bodies allow 8192 UTF-8 bytes, up to 16 recipient IDs and 8 references; limits live in [room types](../apps/server/internal/storage/room_types.go) and the [admission policy](../apps/server/internal/orchestration/policy.go).

Message references use `{kind:"message",messageId}` within the Room. File references can select any known team Resource revision; server resolves excerpts and identity. Admission freezes actual author, references, Agent, Resource identity, environment, model revision and budget atomically. The writable baseline is selected when an ordinary writer acquires its lease; it need not equal an older evidence version.

Message reads optionally include `workOrigin:{workItemId}` only when the message is the exact recorded origin of a task linked to that Work Item in the same Room. This read-only projection supports compact activity display without requiring task history to load; it neither changes original authorship/content nor classifies later user inputs as starts. Missing historical author names remain unknown. New human task origins snapshot the known requester's name when their recorded author ID matches.

Idempotency is user × Room × requestId. Identical canonical intent replays the original receipt before route/configuration changes; changed intent returns 409 `request_conflict`. Preserve the exact body/ID after an uncertain response. `runnerRevision` is required when a team Agent uses the saved Room route; see [routing](#runners-and-source-snapshots).

## Orchestration journal

GET `/api/rooms/:id/orchestration?after=seq&through=seq&limit=20` returns `{items,through,nextAfter,hasMore}` to any enabled team member. The endpoint is read-only and uses the same Room visibility as messages and tasks. Each item has a monotonic `seq`, a source `trigger`, a versioned `decision` and zero or more immutable `mandates`. [Orchestration types](../apps/server/internal/orchestration/model.go) own the wire fields. Original text remains at the referenced Message or task request; grant summaries omit instructions, model configuration and credentials.

Explicit commands include `trigger.request:{scope,id,hash}`, identifying the original actor-scoped receipt and canonical request hash. A Room receipt's scope is its Room ID; task scopes match their create/operation receipts. Automatic task starts instead record `actorKind:"system"`, `actorId:"automation"` and `trigger.automation:{policyId,policyRevision,evaluationId,sourceEventIds}` with no invented human request. Their decision version is `camp-event-v1`, and their Mandate names the authorizing member through `requestedByUserId`. Task work references include their admitted `revision`, and `trigger.inputRevision` identifies the committed input frontier when nonzero. Task Mandates include their shared `baseCommit`; ordinary Run baselines are selected later and are omitted. These fields are absent from older records when they were not captured; reads, upgrades and replays never backfill them from today's mutable work.

`after` is exclusive and defaults to zero. Omitting `through` captures the current Room journal frontier; carry it with `nextAfter` to finish a stable traversal while work continues. Pages contain at most 20 whole records and 64 KiB of encoded JSON including escaping. Limits of zero use the default. Invalid, duplicate or out-of-range numeric parameters return 400 `invalid_page`; a missing Room returns 404. Sequence gaps are valid because other Rooms share the journal sequence.

Room sends record `accept` or `ignore` (`discussion`/`post_only`). Task creation and continuation record `accept`; explicit inputs and answers record `context`; cancellation records `stop`. Mandates summarize grants on accepted Room requests and task start/continuation. They do not replace current permission checks. Task `maxRounds` and `maxModelRequests` are total approved ceilings at that command, not a remaining allowance; single-Run grants have one round and no separate task request ceiling.

The source event, decision, grant summary and work creation/input/operation commit atomically. Replays append nothing, and refused commands leave no admission record. Existing send/task response shapes and request hashes are unchanged. The journal covers commands accepted after its migration; historical decisions are not reconstructed. Agent/system output, handoffs and completion notifications cannot admit new work through this endpoint. See the [architecture mapping](architecture.md#event-admission-and-orchestration) for the implemented boundary.

## GitHub observation

Enabled team members can read Room sources and events, dismiss a pending event, or explicitly confirm it through the existing task-create API. Only current administrators can create, disable/re-enable or rotate a source. Setup is scoped to one Room and repository; source deletion and repository/Room retargeting are not offered. [GitHub observations](github.md) owns the operator workflow, supported event actions and private credential lifecycle.

| Method | Route | Body or result |
| --- | --- | --- |
| GET | `/api/rooms/:id/github-sources` | `{sources}` with safe configuration, revision and observed repository ID |
| POST | `/api/rooms/:id/github-sources` | `{requestId,repository,secret}` → `{source}` |
| PATCH | `/api/github-sources/:id` | `{requestId,revision,enabled?,secret?}` → `{source}` |
| GET | `/api/github-requests/:requestId` | Original safe configuration/dismissal receipt, scoped to the signed-in actor |
| GET | `/api/rooms/:id/github-events?state=pending&after=0&through=seq&limit=20` | `{items,through,nextAfter,hasMore}`; no event bodies in list items |
| GET | `/api/github-events/:id` | `{event}` with original normalized body and current decision |
| GET | `/api/github-events/:id/payload` | Exact original JSON as an attachment |
| POST | `/api/github-events/:id/dismiss` | `{requestId,reason}` → `{event}`; one immutable decision |
| POST | `/api/hooks/github/:sourceId` | Signed GitHub JSON → `{eventId,status,duplicate}` after durable commit |

Source writes and dismissal require a stable actor-scoped request ID. Reusing it with changed intent returns `request_conflict`. Safe receipts omit credential text and opaque private references. Source settings use expected-revision CAS; pending decisions use the immutable event ID and one final decision. A decided event returns 409 `event_decided` for another confirmation/dismissal. Disabled sources return 409 `source_unavailable` at ingress and confirmation. Re-enabling does not replay old deliveries or start pending events.

Only one source per Room and case-insensitive repository address can be enabled. Disable it before creating or enabling a replacement; competing activation returns 409 `source_conflict`. Replacement preserves all earlier source IDs, pinned repository identities, originals, decisions and receipts. A Room retains at most 32 sources, including disabled sources.

Event `state` is `pending` (default), `all`, `accepted`, `dismissed` or `ignored`. Pagination uses exclusive `after`, a captured `through` frontier, at most 20 whole items and 64 KiB of encoded JSON including its newline. The event frontier is stable; human decision states are current. Detail bodies are independently bounded at 64 KiB before JSON encoding. Repository names are case-insensitive; the first valid delivery additionally pins the numeric repository identity.

The webhook endpoint is a separate route tree, with the same HTTPS/localhost transport rule and outer Host policy. It requires exactly one each of `X-Hub-Signature-256`, `X-GitHub-Delivery`, `X-GitHub-Event` and JSON Content-Type. Cookie, Origin and Authorization are refused. Signature verification covers exact body bytes before parsing. The source and verified private credential revision are rechecked transactionally. Invalid signatures return 401; invalid/oversized payloads return 400/413. A delivery GUID or fingerprint replay creates no new event, decision or work. Changed content under an existing GUID conflicts.

Task POST `/api/rooms/:id/tasks?view=summary` additionally accepts optional `sourceEventId` with `inputProtocol:1`. The ID must select a pending event in this Room from a currently enabled source. Normal task permission, participant, route and model checks still apply. The human-authored `goal` stays separate from the complete source context copied into the input log. The response task and admission trigger retain `sourceEventId`. Confirmation, inputs, work admission and receipts commit atomically; failure leaves the event pending. This adds no authority to external sender identities and leaves old task request hashes unchanged.

## Feishu observation

The [Feishu source guide](feishu.md) owns setup and source behavior. Team reads require an enabled member; source configuration requires a current administrator. All configuration writes use actor-scoped stable request IDs and exact expected source revisions. Credentials are write-only and stay in private Server files.

| Method | Route | Body or result |
| --- | --- | --- |
| GET | `/api/rooms/:id/feishu-sources` | `{sources}` with safe settings |
| POST | `/api/rooms/:id/feishu-sources` | `{requestId,appId,tenantKey,chatId,verificationToken,encryptKey}` → `{source}` |
| PATCH | `/api/feishu-sources/:id` | `{requestId,revision,enabled?,verificationToken?,encryptKey?}` → `{source}`; rotate the token and key together |
| GET | `/api/feishu-requests/:id` | Original safe actor-scoped operation receipt |
| POST | `/api/feishu-requests/:id/cancel` | Returns the existing receipt or commits a cancellation rejecting a delayed write |
| GET | `/api/feishu-events/:id` | `{event}` with immutable normalized text and external identities |
| GET | `/api/feishu-events/:id/payload` | Exact encrypted original, downloaded as JSON |
| POST | `/api/hooks/feishu/:id` | Encrypted Feishu delivery or encrypted URL verification challenge; webhook authentication only |

A source is permanently scoped to `appId`, receiving `tenantKey` and `chatId`. Only one source for that scope can be enabled within a Room; up to 32 retained Feishu sources are supported. Identifiers use at most 96 ASCII letters, digits, underscores or hyphens. A new source or explicit replacement is required for a different scope. Secret pairs contain 16–256 bytes per value and never appear in safe source, event or request-receipt JSON.

Ordinary webhook requests require one JSON content type and one each of `X-Lark-Request-Timestamp`, `X-Lark-Request-Nonce` and `X-Lark-Signature`. Signatures compare SHA-256(timestamp + nonce + EncryptKey + exact body) in constant time. Encryption is AES-256-CBC with SHA-256(EncryptKey), a prefixed 16-byte IV and PKCS#7 padding. The decrypted v2 `header.token` must also match. An encrypted `url_verification` challenge authenticates its top-level token and can omit signature headers; no event or execution is created for a handshake. Cookie, Origin and Authorization headers are rejected. Public ingress requires HTTPS except loopback HTTP.

The raw encrypted body is bounded at 1 MiB; normalized text at 64 KiB. Human text `im.message.receive_v1` events can enter the existing automation policy path. Unsupported events and nonhuman/nontext messages are retained as ignored evidence. App, receiving tenant and message chat are checked against source scope inside the same transaction that records the immutable original, receipt, canonical event and any subscribed inputs. The current credential reference and enabled state are rechecked there. Event-ID conflicts reject changed content; re-encryption and an equivalent delivery with another event ID deduplicate.

Canonical Activity items add `feishu:{eventId,appId,tenantKey,chatId,messageId?,threadId?,actorOpenId?}`. Its `eventId` locates the Camp Feishu event and encrypted original. Legacy `externalEventId` stays reserved for GitHub observations. Feishu allowed actors use `senderTenantKey:openId`, preserving external sender identity independently of the receiving tenant and Camp members; policy sender fields allow up to 256 bytes, including the complete pair of 96-byte identifiers. Feishu source policies do not permit external answers or comments. This adapter sends no external messages.

## Event automation

The [event architecture](architecture.md#event-automation) owns durable intake, assessment and authority; [GitHub automation](github.md#automated-work-and-replies) owns setup and external reply recovery. Reads require an enabled member and start no model work. Policies and comment credentials require a current administrator. Subscription and delivery commands require the task requester or administrator. All writes require a stable actor-scoped `requestId`; changed reuse conflicts, and `/automation-requests/:requestId` returns the original safe receipt after an uncertain response.

| Method | Route | Body or result |
| --- | --- | --- |
| GET | `/api/rooms/:id/automation-policies` | `{policies}` with safe policy revisions and credential-configured status |
| PUT | `/api/rooms/:id/automation-policies/:source` | `{requestId,revision,enabled,mode,rules,allowedActorIds,participantAgentIds,firstAgentId,maxTurns,maxModelRequests,dailyEvaluations,dailyTasks,allowAnswers,allowComments}` → `{policy}` |
| GET | `/api/rooms/:id/automation-events?direction=desc&sourceId=room&limit=20` | `{items,through,nextAfter,nextBefore,hasMore}`; recent Activity with bounded previews; ascending `after` traversal remains supported |
| GET | `/api/automation-evaluations/:id` | Safe frozen policy, event/task metadata, Work Item/memory context, assessment and model revision; bounded read-only previews |
| GET | `/api/automation-evaluations/:id/context/:kind?after=0&limit=10` | `{items,nextAfter,hasMore,total,contextLimited?}`; exact frozen `work-items` or `memories` |
| GET | `/api/automation-requests/:requestId` | `{kind,policy?,subscription?,delivery?}`, scoped to the authenticated actor |
| POST | `/api/automation-requests/:requestId/cancel` | Returns the original receipt if committed, otherwise a durable `cancelled` receipt that prevents a late write |
| GET | `/api/tasks/:id/subscriptions` | `{subscriptions}` |
| PUT | `/api/tasks/:id/subscriptions` | `{requestId,revision,sourceId,threadKey,enabled}` → `{subscription}` |
| GET | `/api/tasks/:id/deliveries?cursor=...&limit=20` | `{deliveries,nextCursor}`; descending bounded pages with complete frozen bodies and any external receipt |
| POST | `/api/automation-deliveries/:id/reconcile` | `{requestId,revision}` → `{delivery}`; requests read-only reconciliation |
| POST | `/api/automation-deliveries/:id/resend` | `{requestId,revision}` → `{delivery}`; explicitly permits another send |
| PUT | `/api/github-sources/:id/comment-credential` | `{requestId,revision,token,actorId}` → safe policy receipt; token is write-only |

The evaluation view returns whole frozen `workItems` and `memories` with `totalWorkItems`, `totalMemories`, `workItemsOmitted` and `memoriesOmitted`; its encoded response including the newline stays within 128 KiB. Context paging reads the original stored candidate arrays rather than current Work Items or memory heads. `after` is the count already read, starting at zero; carry `nextAfter` until `hasMore:false`. It may equal the frozen array length, and cannot exceed it. Pages contain at most ten whole snapshots and 128 KiB of encoded JSON including its newline. Unknown kinds, negative or duplicate indexes, and oversized limits return `invalid_page` (400). A single oversized retained record fails explicitly rather than being shortened. Work Item status, recent task/decision excerpts, Action status and memory revision/content remain exactly as frozen, including their explicit context-limit indicators. Source bodies stay at their owning event endpoint; credentials and execution configuration are excluded. These reads do not request a model or admit work.

Activity defaults to the original ascending sequence order when `direction` is omitted or `asc`: use exclusive `after` (default zero) and carry `through` plus `nextAfter`. For recent events, use `direction=desc`, omit `before` and `through` on the first request, then carry its `through` frontier and exclusive `nextBefore` as `before` to load older events. `before` must be positive and no greater than `through`; first-page omission includes the event at `through`. Descending reads reject `after`, ascending reads reject `before`, and the unused next cursor is `null`. Omitting `through` captures the current Room frontier; new arrivals are excluded from subsequent frozen pages. Refreshing or changing the source starts a new traversal. Optional `sourceId` filters matching rows before ordering/limiting: empty means all sources, `room` and `task` select built-in sources, and a retained GitHub source ID must belong to the Room. Disabled source history remains readable. Invalid or duplicate cursor, order or source parameters return `invalid_page` (400).

Activity pages contain at most 20 whole events and 64 KiB of encoded JSON including its newline. `status`, `reason` and recorded assessment fields remain current within the frozen event identity range. Optional `preview` summarizes visible Room/task messages or the original GitHub title in at most 320 UTF-8 bytes. `assessmentOutcome` is the recorded `accept`, `ignore` or `unavailable` outcome; `assessmentReason` is a summary of its reason in at most 512 bytes. Previews collapse whitespace and use `...` when shortened. They are read projections, never authority or replacements for the complete source/assessment; `content` stays empty in Activity items. Reading a suggested acceptance never changes an event's status or starts work.

Policy `:source` is `room` or a GitHub source ID belonging to that Room. The initial expected revision is zero; subsequent writes compare the observed revision. `mode` is `observe` or `auto`. Enabled policies require nonempty rules, allowed actors, approved participants, a first Agent among those participants and positive daily limits. Rules are bounded at 8192 UTF-8 bytes, actor IDs at 128 entries and participants at 8 distinct Agents. GitHub actor IDs are immutable positive numeric IDs and never map to Camp membership. Task allowance defaults are 6 turns and 24 model requests, with maxima 24 and 96. Daily assessment/task limits are at most 1000/100 and reset at 00:00 UTC. An assessment slot is charged before model access; changing a policy does not refund it. These are request bounds, not billing caps.

Every policy edit freezes a new intake frontier. Earlier observations are never silently executed after enabling Auto. Each assessment freezes policy/model revisions, a bounded event batch and the related task frontier. Late events wait for a later batch. Completion revalidates current authority and produces at most one finite task. Observation and unavailable outcomes create no task; stale results require attention rather than a model replay. Human GitHub confirmation and automatic acceptance share the external event's one final decision.

Subscription inputs are context under the task's existing grant. They do not answer questions, increase allowances or resume ended work. A task can follow at most 32 conversations. Terminal tasks reject enabling a subscription. Room Threads must belong to the task's Room; GitHub Threads must be observed from a source in that Room. Task/input responses may add `automationPolicyId`, `automationPolicyRevision`, `automationScope`, `automationSourceEventId`, `messageId`, `sourceEventId`, `authorKind`, `externalActorId` and `partIndex`. Task `sourceEventId` retains the original GitHub observation ID; `automationSourceEventId` identifies its canonical event. Original message identity remains explicit; external originals have independent input IDs and an empty `authorUserId`. External answers retain `answerSourceEventId` and `answeredByExternalActorId`, with no forged Camp user attribution.

GitHub answers require an enabled Auto policy, `allowAnswers`, an allowed human actor, the exact current `/camp answer <questionId> <answer>` command and a matching followed Thread. They can only continue the current unanswered question within the remaining finite grant. Invalid, stale or ambiguous commands authorize no work.

Delivery pages retain complete bodies, default to at most 20 items and cap encoded responses at 320 KiB; their opaque cursor is bound to the task. Delivery states are `pending`, `sending`, `sent`, `unknown`, `reconcile`, `failed`, `blocked` and `withdrawn`. Stale questions and cancelled tasks withdraw queued sends without making another POST; `withdrawn:true` also stays set during any later read-only reconciliation. Withdrawal does not assert that an earlier uncertain send had no effect. Historical unknown attempts allow reconciliation but never another POST. Recovery commands compare delivery revision and operate only on recoverable states. `attempts` retains each frozen actor, dispatch revision/time when captured, and its `sending`, `sent`, `failed` or `unknown` outcome; dispatch records alone do not prove a POST succeeded. `receipts` retains deduplicated confirmed comment IDs, actors and canonical URLs. `receiptScanLimited:true` means the retained/scanned evidence may omit matching comments. Neither array exposes private credential references. A delivery allows at most 32 dispatch attempts and retains up to 32 confirmed receipts. Unknown sends are reconciled by exact marker/body and any recorded dispatch actor, without another POST; absence is still unknown. Explicit resend can create a duplicate and requires that consequence in the UI. Comment credentials use the policy revision and remain in private Server files; responses and operation receipts omit the token and opaque file reference.

## Work items

All enabled team members can read Room matters and their evidence. The owner or a current administrator can change a matter. Mutations require a stable actor-scoped `requestId`; operation bodies retain their expected `revision` after uncertain responses. Exact replay returns the original receipt before current state, route or model readiness checks. Changed request content conflicts; cancellation commits a tombstone only when the original operation has not committed.

| Method | Route | Body or result |
| --- | --- | --- |
| GET | `/api/work-items?scope=mine&view=active&roomId=...&cursor=...&limit=20` | Cross-Room current summaries `{items,nextCursor,hasMore}` |
| GET / POST | `/api/rooms/:id/work-items` | Page `{items,nextAfter,hasMore}` or create input → `{item}` |
| GET | `/api/rooms/:id/work-item-defaults?policyId=...` | Advisory finite allowance, expiry and participants; starts no work |
| GET | `/api/work-items/:id` | `{item}` with bounded detail previews |
| POST | `/api/work-items/:id/decisions` | Versioned operation → `{item}` |
| POST | `/api/work-items/:id/actions/:action` | Typed human intent → `{item}`; same revision and actor-scoped recovery |
| GET | `/api/work-item-requests/:id` | Original actor-scoped `{item}` receipt |
| POST | `/api/work-item-requests/:id/cancel` | `{cancelled}`; read the original receipt when false |
| GET | `/api/automation-events/:id` | `{event}` with complete canonical source text and original-source references |

[Work Item types](../apps/server/internal/orchestration/workitems.go) own fields. Creation requires `requestId` and `goal` (up to 8192 UTF-8 bytes). Optional `completionCriteria` selects criteria-based completion; omission sets `completionMode:"human"`, requiring human confirmation to close. Optional `dueAt` is a business date and grants no execution authority. `expiresAt` is the finite authorization expiry; `deadline` retains its compatibility meaning as the same expiry. An omitted expiry defaults to 30 days; a supplied expiry must be future RFC3339 within one year. The defaults read provides a server-owned proposal rather than a grant.

Creation accepts up to 20 distinct same-Room `evidenceIds`, an optional owned Room `policyId` with `policyRevision`, `sourceEventId` or `messageId`, explicit `follow`, and explicit `start` with participants/first Agent. Recording alone starts no work. Source association, explicit conversation following, initial finite execution and the creation receipt commit together; a failure leaves no partial matter or task. Source identity and Room membership are rechecked inside the transaction. Exact replay returns the original receipt before later state checks.

Default cumulative ceilings are 12 assessments, 4 tasks, 96 reserved model requests, 16 external dispatch attempts and 8 timer follow-ups. Maxima are 1000, 100, 9600, 1000 and 100 respectively; follow-ups alone may be zero. New source policies expose the same `itemBudget`; existing matters retain their own saved ceilings. Policy edits and daily resets do not refill matters.

Typed actions are `start`, `answer`, `answer_continue`, `wait`, `complete`, `stop`, `delegate`, `attach`, `follow`, `unfollow` and `authorize`. They require `requestId` and the expected matter `revision`, plus action-specific fields. `comment` is optional; human answers use `content`. Audit records distinguish a supplied human statement from a deterministic operation description using `reasonSource`.

`start` selects participants and the first Agent, creates a new finite task and reserves its entire request allowance. An active task's `answer` also supplies `taskId`, `taskRevision` and `questionId`; the original task owner validates the question and continues within its remaining grant, without adding allowance. A matter-level `answer` records the person's answer without starting execution; `answer_continue` explicitly admits a new finite task in the same transaction. `questionKind:"completion"` identifies an explicit human completion request; its affirmative action is `complete`, independently of the question's text. Ordinary questions use `questionKind:"judgment"`; no question omits the kind. `wait` requires `waitCondition` and optionally `resumeAt` before authorization expiry. `attach` associates a source without implicitly following it. `follow`/`unfollow` refer to an observed source conversation. `delegate` explicitly changes the binding using the current policy revision and records a new intake frontier; disabling delegation grants nothing. `authorize` changes cumulative ceilings covering all usage/reservations and optional future expiry, without starting work. Human `complete` records acceptance, while unresolved external actions still block completion.

`stop` first records durable intent, retires pending waits/follow-ups and requests cancellation through the existing finite task lifecycle. The API projects `stopping` until cancellation settles, then `cancelled`. No new assessment, model request or external action is authorized during stopping. Historical SQL states and originals remain intact. Completed/cancelled matters cannot reopen. Invalid commands return `invalid_work_item` (400); stale revisions, finished matters and exhausted allowance return `revision_conflict`, `work_item_finished` or `work_budget_exhausted` (409).

The compatibility `decisions` endpoint retains the advisory vocabulary and old receipt bodies, including explicit `request_judgment`, `ignore` and `evidence`. Ordinary UI actions use the typed routes. An advisory ignore only records a decision; it does not stop the matter.

The cross-Room list requires a currently enabled account. `scope=mine` (default) selects the current owner's matters; `scope=team` retains existing team-wide visibility, including matters owned by disabled accounts. `view=active` (default) selects unfinished matters including stopping; `done` selects completed/cancelled matters; `all` selects both. `needs_you` selects only the current owner's actionable human questions or attention: an unanswered matter question/attention without an active task, or an unanswered active task question or blocked/interrupted task with no active run. Ordinary open, waiting, queued and running work does not demand attention. Administrator access to another owner's matter does not make it `needsYou`.

Optional `roomId` filters before pagination; a missing Room returns 404. Every cross-Room view sorts `needsYou` first, then immutable sequence descending. Its opaque cursor binds the actor, scope, view and Room and retains the first page's maximum sequence, excluding later creations. Carry `nextCursor` unchanged; an empty string and `hasMore:false` mark the end. Current status/permissions remain live, so refresh starts a new traversal after matters move between views or attention groups. Limits are 1–20 items (default 20) and 320 KiB of encoded JSON including the newline. Summaries include Room name, owner, goal, status/revision, due date, question, current/latest execution and exact saved result projections; `needsYou` uses the same predicate as filtering/ordering. History arrays, budgets and grants are omitted. No Room fanout is required. Unknown, duplicate or invalid parameters, noncanonical/cross-account/filter-mismatched cursors and oversized retained summaries fail with `invalid_page` (400). These reads create no Inbox entries, tasks, model requests or state transitions.

Room list pages retain exclusive nonnegative `after` and at most 20 items. With `state=all|needs_attention|active|done`, filtering occurs before pagination and sequence order is descending; `nextAfter` is the last included sequence. Questions/attention from the active task count as needing attention, and stopping counts as active. Omitted `state` preserves the original ascending traversal. Duplicate or invalid state parameters fail with `invalid_page`.

Detail previews contain at most 20 evidence/decision/task/timer/action records; `historyLimited:true` directs callers to complete history. `execution` presents the current/latest finite task, question and revision. `result` presents the latest saved task checkpoint or published Room version with summary, resource, commit and up to 20 changed-file entries; `filesLimited` directs readers to the exact-version Library. Deletions remain identified as changes. Execution and result projections select only their display facts from related Tasks and the exact saved Run; list/detail reads do not materialize frozen Agent instructions, execution output or file bodies. These read-only projections never enter the saved matter authorization or imply approval/delivery. Canonical events retain immutable facts while status fields reflect current processing. `workItemId` and `causationId` identify internal follow-up targets and their source fact, task, timer or Action receipt. Reading any of these endpoints starts no model work. Work Item task grants continue to use current Agent/Runner/Resource permissions, frozen task input frontiers and existing publication checks.

## Work item history

GET `/api/work-items/:id/history/:kind?cursor=...&limit=20` reads retained `evidence`, `decisions`, `tasks`, `timers` or `actions` beyond the bounded Work Item detail preview. It requires an enabled team member and returns `{items,nextCursor}`. Items retain each kind's complete record shape; evidence contains the original source event ID, and source originals remain available through their owning event endpoint. Reads admit no work and make no model request.

The first page freezes its monotonically assigned record identity frontier. Carry its opaque `nextCursor` unchanged to load later records; an empty string marks the end. Cursors are bound to the exact Work Item and history kind. Later insertions are excluded until a fresh traversal, while task, timer and delivery status remains current. Pages return at most 20 whole records and 320 KiB of encoded JSON including its newline. A single oversized retained record fails explicitly rather than truncating it. Invalid, repeated or cross-item/kind cursor parameters return `invalid_page` (400).

## Room memory

Memory is Room-scoped, team-visible quoted evidence, never execution authority. Reads require a current enabled member. Creation records that member as author; supersession and retraction require the author or a current administrator. All writes carry actor-scoped stable request IDs. Exact replay returns the original response before later revision/provenance checks; changed reuse conflicts. Recovery reads expose only that actor's receipts, and cancellation either returns an already committed receipt or records a tombstone that rejects a late write.

| Method | Route | Body or result |
| --- | --- | --- |
| GET | `/api/rooms/:id/memories?q=...&limit=10` | `{memories}`; current active revisions only |
| POST | `/api/rooms/:id/memories` | `{requestId,revision:0,content,provenance}` → `{memory}` |
| GET | `/api/memories/:id?revision=...` | `{memory}`; omitted revision selects current; a positive revision selects exact history |
| PUT | `/api/memories/:id` | `{requestId,revision,content,provenance}` → `{memory}`; append a superseding revision |
| POST | `/api/memories/:id/retract` | `{requestId,revision}` → `{memory}`; append a terminal retraction |
| GET | `/api/memory-requests/:id` | `{kind,memory?}`; the original actor-scoped receipt |
| POST | `/api/memory-requests/:id/cancel` | `{}` → the existing receipt or `{kind:"cancelled"}` |

Content allows 8192 UTF-8 bytes. Provenance is exactly one of `{kind:"human_note"}`, `{kind:"message",messageId}`, `{kind:"source_event",sourceEventId}` or `{kind:"file",resourceId,commit,path}`. A note is explicitly human-authored. Messages/events and Resources must belong to the memory's Room; file commits are exact retained 40-character revisions, paths are safe relative files, and HTTP admission checks the saved file exists. Supersession preserves the original author and records the current editor separately. Revision mismatch or an attempted edit of retracted memory returns `revision_conflict` (409); retraction does not erase historical evidence or restart work.

Query `q` allows at most 512 UTF-8 bytes and matches a case-insensitive ASCII substring (other Unicode text matches exactly under SQLite's built-in `lower`). Empty query lists recent active revisions. Limit defaults to 10 and is at most 20; whole results stop before the encoded `{memories}` response plus newline exceeds 64 KiB. Query and revision parameters may occur only once. Retrieval makes no model request, and retracted/superseded revisions are excluded from search. Exact lookup can still read historical revisions, with `current:false` for a superseded revision. Frozen assessment/Run snapshots retain exact memory IDs, revisions, content and provenance even after later edits; their content cannot widen execution authority.

## Runs and events

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/workspace/status` | Execution availability and actionable reasons |
| GET | `/api/rooms/:id/runs` | `{runs}` |
| GET | `/api/rooms/:id/runs?view=timeline` | `{items,nextCursor}` bounded display metadata; `cursor?,limit?` |
| GET | `/api/runs/:id` | `{run}` with captured/published files and check evidence |
| GET | `/api/runs/:id?view=summary` | `{run}` with bounded display metadata |
| GET | `/api/runs/:id/reply` | `{message}` with the exact Agent reply, or null until recorded |
| GET | `/api/runs/:id/events?after=seq` | `{events}` persisted activity pages |
| GET | `/api/runs/:id/patch` | Immutable patch for captured changes |
| POST | `/api/runs/:id/cancel` | `{}`; requester/admin |
| GET | `/api/workspace/events?after=cursor` | SSE with persistent IDs; Last-Event-ID supports reconnect |

Room execution timelines use `view=timeline` without loading full Run snapshots. Pages contain at most 20 complete summaries and 320 KiB of encoded JSON including the newline. Their Room-bound ascending `(createdAt,id)` cursor retains the initial creation frontier; later Runs require a refresh, while status and queue observations remain current. Invalid, duplicate or unknown timeline query parameters and cross-Room cursors return `invalid_page` (400). Summaries retain identity, thread/origin/Task links, Agent identity/access, Runner, state/timestamps, session mode/generation, errors, saved-result identity and check facts. `fileCount` is the complete changed-file count; `files` includes at most 20 exact path/status previews and `filesLimited` identifies additional entries. Output, check output, file bodies, instructions, references, memories, model configuration and grants remain in separately requested full details. Timeline reads preserve a Manager’s unpersisted terminal result and reapply the page bound after that overlay. They create no execution or state transition. Omitted `view` retains the original full Room Run list. Single-Run `view=summary` uses the same whitelist and bound, including a Manager’s retained terminal result and current queue reason. It accepts only the single `view=summary` query; invalid, duplicate or additional parameters return `invalid_page` (400). Omitted `view` on `/api/runs/:id` remains the complete original.

`/api/runs/:id/reply` selects only the recorded Agent reply matching that Run and its Room; it never scans the conversation or materializes execution output. An existing Run without a reply returns `{message:null}`, and a missing Run returns 404. The returned Message preserves its original content, authorship, references and timestamps. Summary, reply and event reads require the same enabled team member access as full Run reads; they create no messages, approvals or execution.

Run states are `queued`, `preparing`, `running`, `checking`, `cancelling`, `succeeded`, `failed`, `cancelled`, `interrupted`. Workspace Agent availability reads only Run identity, Agent identity and indexed state; active executions take precedence over queued requests. The local Manager retains actual in-process state and excludes queued rows whose terminal result remains in its unsaved overlay. Terminal persistence creates one completion Message; process exit alone is not acceptance. SSE consumers deduplicate IDs and refetch affected entities. Session revocation ends streams; browser/window closure does not end work. Message read positions differ from the event cursor.

## Library and publication

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/library` | `{resources,rooms}` including immutable/source metadata |
| GET | `/api/resources` | Working resources |
| POST | `/api/resources` | Admin Server-directory import `{name,path,environment?}` within operator allowlist |
| GET | `/api/resources/:id` | `{resource}` including configuration `revision` |
| PATCH | `/api/resources/:id` | Admin `{expectedRevision,name?,environment?}`; atomic configuration precondition |
| GET | `/api/resources/:id/files?commit=...` | `{files,commit}` |
| GET | `/api/resources/:id/file?commit=...&path=...` | Exact attachment bytes |
| GET | `/api/resources/:id/archive?commit=...` | ZIP of one explicitly selected retained revision |
| GET | `/api/resources/:id/history?before=seq` | `{publications}`, descending page |
| GET | `/api/resources/:id/patch?from=...&to=...` | Diff between retained exact revisions |
| PUT | `/api/resources/:id/files` | `{requestId,expectedCommit,path,content}` |
| POST | `/api/resources/:id/restore` | `{requestId,expectedCommit,commit}` |
| POST | `/api/resources/:id/copy-file` | `{requestId,expectedCommit,path,sourceResourceId,sourceCommit,sourcePath}` |

Resource `revision` versions configuration (name/environment), independently of the files' `currentCommit`. Read it before PATCH and send `expectedRevision`; omission returns 428 `revision_required`, an invalid value returns 400, and a stale value returns 409 `resource_conflict`. Only the matching configuration update increments it and emits an event. Existing resources migrate to revision 1 without changing their files or settings. Read back the current Resource after an uncertain response rather than replaying a write blindly. Older browser clients must refresh to use the current settings editor.

Publication returns `{resource,publication}`. Human edits/restores share the durable Resource writer lease with ordinary Agent publication and require baseline CAS. 409 `resource_busy`/`revision_conflict` must preserve the private draft. Actor × Resource × request receipts prevent duplicate publication. Restore creates a new commit; copy preserves binary bytes; originals cannot become writable in place. The text editor accepts UTF-8 up to 1 MiB. File capture limits are defined by [resource ingestion](../apps/server/internal/resources/content.go): 10,000 files, 8 MiB/file and 128 MiB total, with credential-name/path/type rejection.

Publications distinguish `edit`, `restore`, `agent`. Human checks are `not_run`; Agent success is `passed` only with complete matching configured check evidence, otherwise `not_configured`. Unconfirmed capture never claims retention. Git/SQLite remain authority; a LayerFS checkpoint is not a shared save.

Whole-resource ZIP downloads require exactly one explicit retained 40-character `commit`, including when downloading the current saved files. The endpoint uses the same enabled-member read access as individual files; it does not publish, approve or execute work. Entries retain the selected revision's relative paths, binary bytes and executable bits, without Git metadata, a source-directory prefix or personal editing drafts. The archive follows the resource file-count and byte bounds above, with a separate 256 MiB output bound for ZIP headers and compression overhead. Unsupported paths, links, credential names and names containing Windows drive/alternate-stream colons are rejected. The complete archive is constructed before download headers are sent; a failed export returns an ordinary API error rather than a successful partial download. The attachment filename includes the selected revision prefix.

An unconfigured environment is `{}`. Configured environments have `kind:"node"` or `kind:"command"`; missing/null `checks` means no configured checks. A null update clears configuration. [Profile validation](../apps/server/internal/projectenv/profile.go) owns accepted fields/commands. Configuration presence does not prove runtime readiness. The environment update API cannot detect an unseen concurrent admin edit.

## File uploads

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/rooms/:id/uploads` | Multipart `requestId` and one `file` → `{reference}` |
| GET | `/api/rooms/:id/uploads?before=...` | `{references,nextCursor}` originals |
| GET | `/api/rooms/:id/uploads/:requestId` | Actor-scoped `{reference}` receipt |

[Upload parsing](../apps/server/internal/httpapi/room_uploads.go) accepts exactly one unencoded requestId part (up to 128 bytes) and one file part with a safe raw basename. The file limit is the resource per-file bound above; extra multipart fields/parts fail. No archive extraction, host path or executable permissions are accepted. User × Room × upload ID binds filename and byte hash. Exact concurrent or lost-response retries converge; changed content conflicts. Upload and message request identities are separate, and uploading never starts Agent work or text preparation.

Original Git storage precedes the Resource/receipt/event transaction. A failed registration compensates only its unregistered candidate, never a possibly committed original. Reload checks receipts, not a claim that browser File bytes survived. Removing a draft reference does not delete the saved original; late uploads stay with their original Room/Thread. No original-deletion or general retention UI is provided.

Markdown files are rendered directly. Readable UTF-8 files can be viewed as source; other files remain immutable original attachments. File references contain the Resource, exact commit and path, without a separate parsed-artifact identity.

Originals mount read-only at `/references/<index>/<path>`. Agents read the selected bytes through ordinary file tools; the Server does not create extracted text, manifests or document-specific citation locations. Selecting an opaque attachment does not establish that an Agent has read its contents. The [synthetic Markdown example](../examples/documents/README.md) supplies readable sources.

## Reviews and delivery

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/reviews?roomId=...&resourceId=...` | `{reviews}` with optional filters |
| GET | `/api/reviews/:id` | `{review,events,replayed:false}` |
| POST | `/api/rooms/:id/reviews` | `{requestId,title,resourceId,path,commit,reviewerUserId}` |
| POST | `/api/reviews/:id/actions` | `{requestId,revision,action,comment?,commit?,reviewerUserId?}` |

Mutations return `{review,events,replayed}`. Creation pins an existing saved file in the Room's current working Resource at its current commit, with an enabled human reviewer and title up to 200 UTF-8 bytes. Original uploads need a working copy first. A system root Message creates the existing discussion Thread; all transitions append there without rewriting evidence or creating Runs.

| Action | Authority and precondition |
| --- | --- |
| `approve` / `request_changes` | Assigned enabled reviewer; changes require a comment |
| `resubmit` | Requester/admin with current `commit` |
| `reassign` | Requester/admin with enabled `reviewerUserId`; clear prior decision |
| `deliver` | Requester/admin; current submitted version is approved |
| `cancel` | Requester/admin; unfinished review |

Comments allow 8192 UTF-8 bytes. Status is `in_review`, `changes_requested`, `approved`, `delivered` or `cancelled`; the last two are terminal. Self-review is allowed and attributed. Administrators cannot impersonate an assigned reviewer. Any Resource revision change makes an unfinished review `outdated`, even when another file changed. Resubmit/review current bytes before approval/delivery. A delivered commit is permanent; later Room Resource changes cannot redirect it.

Stale `revision` returns 409 `revision_conflict`; invalid transitions return `review_conflict`/`review_finished`. Actor × Room × request ID scopes creation and actions. Identical replay returns the original receipt/events through its revision; refetch for latest state. Changed body conflicts. Review, attributed audit/Message, receipt, SSE event and targeted Inbox entries commit together. `review.updated` and `resource.updated` both require freshness refresh. Agent feedback and `camp_finish` have no approval or delivery authority.

## Personal state

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/bootstrap?clientId=...` | Personal preferences/layout |
| PUT | `/api/preferences` | Personal appearance |
| PUT | `/api/layout` | `{clientId,layout}` for signed-in person/browser |
| GET | `/api/me/drafts?kind=document` | Private draft metadata |
| GET / PUT / DELETE | `/api/me/drafts/:kind` | GET `?key=...`; PUT `{key,value,revision}`; DELETE `{key,revision}` |
| GET / PUT | `/api/me/room-reads` / `/api/me/room-reads/:roomId` | `{reads}` / `{seq}` using message sequence |
| GET | `/api/me/inbox` | `{items}` for mentions, results, tasks and reviews |
| POST | `/api/me/inbox/:id/read` | `{}` |

Draft kinds are conversation/document/review. Responses wrap `{draft}`. An absent draft has null value/revision 0; saves require current revision. Deletion keeps a revision tombstone. Conflicts preserve local text; callers cannot select another user ID. Keys allow 1200 bytes; conversation/review JSON is bounded to 256 KiB, document JSON to 2 MiB and text to 1 MiB. See [personal storage](../apps/server/internal/storage/team_personal.go).

### Personal attention

GET `/api/attention?roomId=...&kind=all&cursor=...&limit=20` returns `{items,nextCursor,hasMore,total,counts:{judgment,review,exception}}`. It requires a currently enabled member. Optional `roomId` filters before paging; an unknown Room returns 404. `kind` is `all` (default), `judgment`, `review` or `exception`. Counts and total cover all current categories in the selected Room, or all Rooms when omitted, independently of category and page. Reads create no notices, grants, decisions, timers, model requests or state transitions.

Each item has a stable source-prefixed `id`, `source` (`work_item`, `review`, `task` or `delivery`), `kind`, `title`, `summary`, `status`, `reason`, numeric `priority`, Room identity/name, source creation/update timestamps and a canonical `target`. Targets use the existing Work or Workspace owner with `roomId` and the applicable `workItemId`, `reviewId` or `taskId`. Linked task questions/recovery appear once on their Work item; external delivery exceptions retain their separate delivery identity and target the existing finite task recovery view. The projection exposes no delivery body, credential, task grant or mutation operands. Titles/summaries are bounded to 512/2048 UTF-8 bytes; `textLimited:true` directs callers to the full source owner. A delivery without its own update timestamp retains its creation timestamp.

Personal responsibility follows the Work owner, independent task requester, assigned valid human reviewer, review requester or external delivery's task requester. Administrator capability does not assign another person's work. Current valid `in_review` versions belong to the reviewer. Outdated versions, unavailable reviewers before approval, requested changes and approved versions awaiting explicit delivery belong to the requester, under the review's existing recovery rules. A recorded current approval remains deliverable if its former reviewer is later disabled. Normal open/waiting/running work, offline automatic recovery and terminal historical Run failures are activity rather than invented unresolved incidents.

Inbox read positions are independent. Reading a notice cannot answer a question, decide or deliver a review, or settle an uncertain action. Rows leave only when their owner's actual business state no longer requires the person's action; a new question can reuse the same stable source identity. Known-unsent comment failures leave when their task is cancelled, parent is stopped or original question is answered/replaced, matching the sender's automatic withdrawal rules. Unresolved unknown attempts still require reconciliation. Confirmed receipts resolve withdrawn unknown deliveries, while pending read-only reconciliation does not demand another simultaneous command.

One SQL read snapshot computes the page and counts under current account authority. Order is exception, judgment, review, then immutable source creation descending and stable ID ascending. The opaque canonical cursor binds the actor, Room, category and last ordering key. Source states remain live; refreshing restarts traversal after responsibility or priority changes. Pages contain 1–20 whole records (default 20) and at most 320 KiB of encoded JSON including the newline. Invalid, unknown, duplicate or mismatched parameters/cursors return `invalid_page` (400). There is no aggregation-owned resolve or dismiss endpoint.

## Settings

Admin GET/PATCH `/api/settings/models` exposes safe fields, `apiKeyConfigured`, opaque revision and source/override metadata. PATCH accepts model/baseUrl/apiKey, clearApiKey, request limits and `reasoningEffort` (`""` for provider default, `off`, `low`, `high`, `max`); keys are write-only. External overrides are locked. Queued work retains its private revision, including reasoning; see [configuration](dsh.md#configuration-and-budgets). Admin GET `/api/settings/environments` and `/api/settings/storage` report execution policy, storage facts and the Server import allowlist.

GET `/api/settings/readiness` refreshes model configuration, revision-specific prior provider observations, connected ready Runner reports and recorded native task success. The top-level `checkedAt` is the refresh time; component timestamps retain the actual observation/self-test time. A refresh performs no dependency probe, model request or admission pause. See [readiness reporting](architecture.md#readiness-reporting). Host Terminal, executable previews and retired VFS endpoints remain absent.

## Runners and source snapshots

Members list Runners and create their own enrollment credentials. Owner/admin controls personal machine changes, grants and source import. Team defaults and Room routes require an administrator. Metadata exposes no machine/model secrets; enrollment tokens are returned once, with revocable metadata retained. Safe public capabilities include optional `taskInputsVersion` and `reasoningEffortVersion` for read-only protocol preflight, without exposing private Runner capabilities or starting work.

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/runners` | `{runners}` with connection, runtime, readiness, slots and version observations |
| GET / POST | `/api/runner-enrollments` | `{enrollments}`; POST `{name}` → `{enrollment,token}` |
| POST | `/api/runner-enrollments/:id/revoke` | `{}`; prevent enrollment/replay |
| PATCH | `/api/runners/:id` | `{name?,enabled?,revoke?,teamShared?}`; revocation permanent |
| GET | `/api/runners/:id/agents` | Exact grants; owner/admin |
| PUT / DELETE | `/api/runners/:id/agents/:agentId` | Grant/revoke; owner/admin |
| GET / PATCH | `/api/execution-defaults` | Read revisions; write `{scope,runnerId,revision}` |
| PUT | `/api/rooms/:id/runner` | `{runnerId,revision}` Room-route CAS |
| GET | `/api/runners/:id/sources` | Owner/admin aliases/revisions/availability; no host paths |
| POST | `/api/source-imports` | `{requestId,runnerId,sourceId,expectedSourceRevision,roomName}` → `{sourceImport}` |
| GET | `/api/source-imports` / `/api/source-imports/:id` | `{sourceImports}` / `{sourceImport}` |
| POST | `/api/source-imports/:id/cancel` | `{}`; can remain cancelling until cleanup |

Omitted Runner patch fields retain current transactional values. Disabling cancels queued/active work; renaming cannot re-enable or undo revocation. Agents expose `ownerUserId,preferredRunnerId`; Runners expose `ownerUserId,teamShared`. Empty owner denotes a legacy team object. Native first-task fields require execution provenance; fixture or unattributed historical success is not upgraded to native success by changing the Runner's current capabilities.

Personal Agent routing stays on its saved target. Team Agent order is explicit Agent target → Room target → team default, falling back only for absent configuration. Admission freezes the target; offline/busy queues never change it. A message's `runnerRevision` checks a saved Room route only when that route is used; replays return their original receipt before checking a changed route.

SourceImport has its own durable identity and revocation, not a Run or model call. Capture creates a central immutable initial commit and new working Room resource, not a second immutable-original Resource row. Later work uses copies and never writes back to the operator's directory. Server-directory admin import is a separate allowlisted API, not permission to browse arbitrary Runner paths.

Installation endpoints are GET `/api/runner-installation`, `/api/runner-package` and `/api/runner-package/checksum`; `?platform=linux-amd64|darwin-arm64` selects the execution target (default Linux), and `?variant=offline` selects its offline package. Metadata includes the selected `target`, human `platform` and supported `platforms` list; availability is specific to that target. Metadata reports fixed `version`, `source` (`github` or `server`), SHA256, compressed bytes, runtime/unpacked bytes and availability. Published Servers embed their Runner descriptor and advertise exact public GitHub URLs without staged packages. A complete matching local mirror takes precedence for installer links; it does not change the installer's fixed runtime identity or public default. Local package routes retain browser authentication. GET `/api/daemon/runtime/:sha256` uses enabled machine credentials, rejects browser cookies and serves an exact retained digest independently of current release metadata. Public GitHub requests carry no Camp credential. [Releasing](releasing.md) owns package publication; [installation](install.md#stage-a-package-mirror) owns explicit mirror staging.

## Collaborations

Any enabled member can start a task, add inputs and answer an open question. Only requester/admin can cancel, continue, add allowance or refresh inactive participants. All task drafts/results remain team-visible.

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/rooms/:id/tasks` | `{requestId,goal,participantAgentIds,firstAgentId,threadRootId?,references?}` → `{task}` |
| GET | `/api/rooms/:id/tasks` | `{tasks}` |
| GET | `/api/tasks/:id` | Legacy `{task,runs}`; `?view=summary` returns `{task}` without histories |
| GET | `/api/tasks/:id/inputs` | `{items,through,nextAfter,hasMore}`; `after?,through?,limit?` |
| GET | `/api/tasks/:id/runs` | `{items,nextCursor}`; `cursor?,limit?` |
| POST | `/api/tasks/:id/inputs` | `{requestId,content}` |
| POST | `/api/tasks/:id/answers` | `{requestId,revision,questionId,content}` |
| POST | `/api/tasks/:id/continue` | `{requestId,revision,agentId?,content?,addTurns?,addModelRequests?,fromLatest?,fromRunId?,participantAgentIds?,refreshModelSettings?}` |
| POST | `/api/tasks/:id/cancel` | `{requestId,revision}` |

Task lists and mutations also accept `view=summary`, omitting `task.inputs` and full Run snapshots. Metadata includes `inputRevision` (highest committed input), `inputBytes` (original content bytes) and `coveredInputRevision` (prefix included in accepted successful rounds). Input pages contain whole originals with task-local `seq` and the original Message ID, author, content and timestamp. Omit `through` to capture the current input frontier; carry it on subsequent pages. `after` is exclusive and `through` inclusive. Pages are capped at 20 entries and 64 KiB of encoded JSON, including escaping; they never truncate an input. Run pages contain bounded display metadata, ordered by creation time and ID, with an opaque task-scoped keyset cursor. Both endpoints require the same enabled-member visibility as task detail. Invalid bounds/cursors return `invalid_page` (400).

The Dashboard uses summary reads and separate history pages. New creation/input/answer/continuation bodies explicitly send `inputProtocol:1` with `view=summary`. This enables finite per-round batches and lifts the lifetime 128-input/64 KiB content cap, retaining 8192 bytes per input. Protocol 0 (field omitted) preserves the old limits and full-prefix rounds. Unsupported versions fail; summary presentation alone does not opt in. Pending browser requests retain their original protocol/body/ID after reload, including old requests without the field. Legacy detail and mutation responses include the accepted input prefix within the old bounds. Oversized histories require pagination (`task_pagination_required`, 409) before any new unpaged mutation; accepted receipts resolve first.

Operations return `{task}` and persist user/scope/request receipts. Identical replay precedes later revision/status checks; changed content conflicts. Presentation does not change the request hash. New receipts reference their original input frontier; legacy receipt bytes remain unchanged. Replay never appends inputs admitted after that receipt. Answers are first-valid-submission wins. Initial allowance is **6 executing rounds and 24 model requests**; one explicit continuation can add at most 24 rounds and 96 requests. Waiting consumes neither. Effective per-Run limits remain in [model configuration](dsh.md#configuration-and-budgets).

States are `queued`, `running`, `waiting_for_person`, `needs_attention`, `publishing`, `completed`, `cancelling`, `cancelled`, `interrupted`. A supplied participant set on continuation explicitly refreshes Agent configurations/routes; ordinary continuation keeps the old grant. Each round freezes inputs/checkpoint. Intermediate Runs use `checksDeferred` and do not publish Room files. Final publication requires checks, cleanup, current authority and original shared-baseline CAS. Conflict retains the draft; `fromLatest` explicitly rebases work with the old draft as read-only evidence, without automatic merging. See [task lifecycle](architecture.md#collaboration-tasks).

`refreshModelSettings:true` explicitly selects the current trusted model configuration and per-Run limits for future rounds. Omission/false retains the original task configuration and legacy canonical body. The choice requires the normal requester/admin continuation authority, is part of its canonical receipt, and appends the existing finite continuation Mandate. It never rewrites earlier Runs, resets usage or increases cumulative ceilings; those require the existing explicit additional-allowance fields. Receipt replay precedes current-setting checks, including when today's connection is unavailable.

Summary task detail also returns optional `recoveryRun:{runId,commit,agentName,fileCount}` for one retained failed/interrupted writer result at the current draft fence. In **Starting files**, the Dashboard defaults to the accepted Task draft and offers this saved work or latest Room files as explicit alternatives. Reviewing saved files opens the exact immutable version in Library, retaining the unfinished continuation form in Work or Workspace. `fromRunId` selects the exact captured result and is mutually exclusive with `fromLatest`. Storage requires an idle task, requester/admin and current execution authority, matching Task/Room/Resource, current draft fence and input baseline, a valid retained result commit and a committed completion with confirmed cleanup. The Server verifies the immutable files before admission. Recovery changes only the unpublished task checkpoint/revision; shared baseline/files, prior draft evidence, old Run, input coverage and cumulative usage remain intact. Partial work has not passed final checks; normal checked completion and publication CAS still apply. Stale/unsafe choices return `invalid_task_recovery`; accepted receipt replay precedes new candidate/file checks, preserving exact recovery across an uncertain response.

## Daemon protocol v2

Machine routes reject Cookie/Origin headers and obey Host policy. Non-loopback connections require TLS; the Runner verifies Server certificates and same-origin URLs. No arbitrary upstream address, port tunnel or cross-machine Store is exposed.

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/daemon/enroll` | `{token,registrationId,credential,name,storageId}` → `{runnerId}` |
| GET | `/api/daemon/connect` | WSS bearer; hello/welcome with protocol and epoch |
| GET | `/api/daemon/operations/:id/task-input-capability` | Owning machine only; executing protocol-1 Attempt → private `{token,url,scope}` |
| GET | `/api/daemon/task-inputs/:id?after=S&limit=N` | Dedicated Attempt read bearer, not machine/browser auth; bounded original input page |
| GET | `/api/daemon/operations/:id/input?part=project\|references` | Exact authorized input digest/length |
| POST | `/api/daemon/operations/:id/finalizing` | `{}` → bounded upload authorization or terminal receipt |
| PUT | `/api/daemon/operations/:id/artifact` | Whole bounded tar with `X-Camp-SHA256` |
| POST | `/api/daemon/operations/:id/complete` | Restricted Result → `{disposition,runId?,status?,reason?}` |
| POST | `/api/daemon/model/:attempt/v1/messages` | Attempt capability in `x-api-key`; frozen Messages JSON/SSE |

The model proxy returns HTTP 403 with `error.code:task_budget_exhausted` only when the task's model request counter has reached its approved ceiling, after current execution authority checks. The denial sends no upstream request and charges no request. Revocation and other authorization failures retain their separate generic errors. Exhaustion is terminal for that request; only an explicit continuation can add an allowance. Task and Run execution errors remain recorded as received; the UI can show exhausted paused-task counters separately without reclassifying a historic failure.

Runner persists identity/credential before enrollment; identical registration replays after lost response, different content conflicts. Revocation prevents further machine access. Fixture execution is explicitly labelled and requires Server `--allow-fixture-runners`, while using the same journal/protocol lifecycle.

Runner capabilities optionally report actual execution `platform`, `workspace`, `sandbox` and `checkpoint` adapters. An unavailable/unopened backend reports no invented backend version. These observations confer no permission and do not establish historical task provenance.

Runner hello advertises `reasoningEffortVersion:1` when it propagates the frozen reasoning mode through the public DSH initialize contract. Explicit modes require this capability during readiness and input preparation; omission remains compatible with provider-default work. An incompatible Runner needs an operator upgrade before explicit-mode work can execute; no model call or automatic retry is made.

Runner hello advertises optional `taskInputsVersion:1`; omission keeps legacy compatibility. New bounded task claims wait for this capability without reserving execution. The frozen command contains `task.inputs:{version:1,after,through}` and no history credential. Only the owning machine may retrieve the stable private credential while executing. Its read endpoint accepts only `after` (exclusive, nonnegative, at most the frozen `through`) and `limit` (1–20). Current authority, lease/deadline, task fence and executing phase are rechecked on every read; reads neither charge execution nor acknowledge inputs. Credential, identity, range, cancellation or revocation failures return no originals. No arbitrary Task ID, URL or origin is selectable by the model. Legacy commands omit the scope and keep their old tools.

Input preparation reserves one worker before admitting another queued Run. A claimed Attempt freezes its conversation identity before heavy I/O, then activates its manifest/session/start command atomically after current permission, lease/deadline and fence checks. Preparing a file does not grant model authority. Cancelled unsent Attempts finish locally; a late builder result cannot install a command. Restart retains the same Attempt and frozen session for an eligible unsent rebuild, or replays the original committed command. Preparation does not renew the execution lease.

Frames are `hello`, `welcome`, `command`, `accepted`, `event`, `event_ack`, `heartbeat`, `grant`, `cancel`, `rejected`. Commands carry stable identities and deadlines. Execution needs durable acceptance and a grant. Renewal is independent of command hash, echoes its causal requestId and uses that request's monotonic send time. Old epochs cannot renew leases. Events are sequenced per operation and ACK only after commit. Reconnect reconciles original operations before new work.

Completion disposition is `accepted`, `committed` or `rejected`; upload acceptance is not publication. Completion identity/canonical manifest hash replays the original final receipt even after lease expiry. Changed content conflicts. Old Attempts, fences, expired authority, cancellation or invalid archives cannot publish. Model access is revoked before upload; corrupt final results seal the operation and release its slot. Uncertain cleanup suspends the Runner atomically with its receipt. Disabled machines can retrieve terminal receipts; revoked credentials cannot. These are idempotent state/publication guarantees, not exactly-once model calls or external effects.

Protocol v2 carries optional `ExecutionSpec.task` and `Result.intent`. One legal coordination decision must match its task, approved participants, call identity, paths and fence. A persisted intent alone cannot restart a prompt. Checkpoint, confirmed cleanup, terminal receipt and successor commit before later dispatch.
