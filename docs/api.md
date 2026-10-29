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

Selected plus mentioned joined Agents produce at most one Run each. With neither, a message is discussion. `postOnly:true` suppresses Agent mentions and cannot accompany explicit recipients. Human mentions notify people. Agent/system output is not an admission source. Joining never replays history. Bodies allow 8192 UTF-8 bytes, up to 16 recipient IDs and 8 references; limits live in [room types](../apps/server/internal/storage/room_types.go).

Message references use `{kind:"message",messageId}` within the Room. File references can select any known team Resource revision; server resolves excerpts and identity. PDF/DOCX references may include `documentId` to pin preparation. Admission freezes actual author, references, Agent, Resource identity, environment, model revision and budget atomically. The writable baseline is selected when an ordinary writer acquires its lease; it need not equal an older evidence version.

Idempotency is user × Room × requestId. Identical canonical intent replays the original receipt before route/configuration changes; changed intent returns 409 `request_conflict`. Preserve the exact body/ID after an uncertain response. `runnerRevision` is required when a team Agent uses the saved Room route; see [routing](#runners-and-source-snapshots).

## Runs and events

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/workspace/status` | Execution availability and actionable reasons |
| GET | `/api/rooms/:id/runs` | `{runs}` |
| GET | `/api/runs/:id` | `{run}` with captured/published files and check evidence |
| GET | `/api/runs/:id/events?after=seq` | `{events}` persisted activity pages |
| GET | `/api/runs/:id/patch` | Immutable patch for captured changes |
| POST | `/api/runs/:id/cancel` | `{}`; requester/admin |
| GET | `/api/workspace/events?after=cursor` | SSE with persistent IDs; Last-Event-ID supports reconnect |

Run states are `queued`, `preparing`, `running`, `checking`, `cancelling`, `succeeded`, `failed`, `cancelled`, `interrupted`. Terminal persistence creates one completion Message; process exit alone is not acceptance. SSE consumers deduplicate IDs and refetch affected entities. Session revocation ends streams; browser/window closure does not end work. Message read positions differ from the event cursor.

## Library and publication

| Method | Path | Contract |
| --- | --- | --- |
| GET | `/api/library` | `{resources,rooms}` including immutable/source metadata |
| GET | `/api/resources` | Working resources |
| POST | `/api/resources` | Admin Server-directory import `{name,path,environment?}` within operator allowlist |
| PATCH | `/api/resources/:id` | Admin `{name?,environment?}`; no revision precondition |
| GET | `/api/resources/:id/files?commit=...` | `{files,commit}` |
| GET | `/api/resources/:id/file?commit=...&path=...` | Exact attachment bytes |
| GET | `/api/resources/:id/history?before=seq` | `{publications}`, descending page |
| GET | `/api/resources/:id/patch?from=...&to=...` | Diff between retained exact revisions |
| PUT | `/api/resources/:id/files` | `{requestId,expectedCommit,path,content}` |
| POST | `/api/resources/:id/restore` | `{requestId,expectedCommit,commit}` |
| POST | `/api/resources/:id/copy-file` | `{requestId,expectedCommit,path,sourceResourceId,sourceCommit,sourcePath}` |

Publication returns `{resource,publication}`. Human edits/restores share the durable Resource writer lease with ordinary Agent publication and require baseline CAS. 409 `resource_busy`/`revision_conflict` must preserve the private draft. Actor × Resource × request receipts prevent duplicate publication. Restore creates a new commit; copy preserves binary bytes; originals cannot become writable in place. The text editor accepts UTF-8 up to 1 MiB. File capture limits are defined by [resource ingestion](../apps/server/internal/resources/content.go): 10,000 files, 8 MiB/file and 128 MiB total, with credential-name/path/type rejection.

Publications distinguish `edit`, `restore`, `agent`. Human checks are `not_run`; Agent success is `passed` only with complete matching configured check evidence, otherwise `not_configured`. Unconfirmed capture never claims retention. Git/SQLite remain authority; a LayerFS checkpoint is not a shared save.

An unconfigured environment is `{}`. Configured environments have `kind:"node"` or `kind:"command"`; missing/null `checks` means no configured checks. A null update clears configuration. [Profile validation](../apps/server/internal/projectenv/profile.go) owns accepted fields/commands. Configuration presence does not prove runtime readiness. The environment update API cannot detect an unseen concurrent admin edit.

## Uploads and documents

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/rooms/:id/uploads` | Multipart `requestId` and one `file` → `{reference}` |
| GET | `/api/rooms/:id/uploads?before=...` | `{references,nextCursor}` originals |
| GET | `/api/rooms/:id/uploads/:requestId` | Actor-scoped `{reference}` receipt |
| POST | `/api/resources/:id/document` | `{commit,path,retry?}` → `{document}` |
| GET | `/api/resources/:id/document?commit=...&path=...` | Latest `{document}` (null if absent) |
| GET | `/api/documents/:id` | Exact immutable preparation artifact |

[Upload parsing](../apps/server/internal/httpapi/room_uploads.go) accepts exactly one unencoded requestId part (up to 128 bytes) and one file part with a safe raw basename. The file limit is the resource per-file bound above; extra multipart fields/parts fail. No archive extraction, host path or executable permissions are accepted. User × Room × upload ID binds filename and byte hash. Exact concurrent or lost-response retries converge; changed content conflicts. Upload and message request identities are separate, and neither upload nor preparation starts work.

Original Git storage precedes the Resource/receipt/event transaction. A failed registration compensates only its unregistered candidate, never a possibly committed original. Reload checks receipts, not a claim that browser File bytes survived. Removing a draft reference does not delete the saved original; late uploads stay with their original Room/Thread. No original-deletion or general retention UI is provided.

[Document storage](../apps/server/internal/storage/documents.go) identifies exact Resource/commit/path/extractor version. Its separate durable serial queue can restart deterministic parsing; result-write retries do not rerun the parser. Terminal states are `ready`, `unreadable`, `failed`, `unavailable`. Explicit retry creates another artifact while queued work and history retain their frozen IDs. Pending preparation blocks Agent submission, not Post-only discussion. Readiness never means that a model has read the evidence.

Originals mount read-only at `/references/<index>/<path>`; prepared text/manifests mount at `/references/.documents/<index>/`. Only known locations in frozen ready artifacts become interactive citations, with labels from saved evidence. PDF locations are physical pages; DOCX locations are nonempty body/table-cell paragraphs, not printed pages. Citations help inspect a claim without proving it. Markdown exports do not turn Camp-specific source links into portable document hyperlinks. Parser bounds, omissions, Linux isolation and installation have one owner in the [document runtime guide](../tools/document-runtime/README.md). The [synthetic example](../examples/documents/README.md) supplies reproducible sources.

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

## Settings

Admin GET/PATCH `/api/settings/models` exposes safe fields, `apiKeyConfigured`, opaque revision and source/override metadata. PATCH accepts model/baseUrl/apiKey, clearApiKey and request limits; keys are write-only. External overrides are locked. Queued work retains its private revision; see [configuration](dsh.md#configuration-and-budgets). Admin GET `/api/settings/environments` and `/api/settings/storage` report execution policy, storage facts and the Server import allowlist.

GET `/api/settings/readiness` refreshes model configuration, revision-specific prior provider observations, connected ready Runner reports and recorded native task success. The top-level `checkedAt` is the refresh time; component timestamps retain the actual observation/self-test time. A refresh performs no dependency/document probe, model request or admission pause. The former POST recheck endpoint is removed. Optional reader availability is handled by document preparation, independently of plain-text work. See [readiness reporting](architecture.md#readiness-reporting). Host Terminal, executable previews and retired VFS endpoints remain absent.

## Runners and source snapshots

Members list Runners and create their own enrollment credentials. Owner/admin controls personal machine changes, grants and source import. Team defaults and Room routes require an administrator. Metadata exposes no machine/model secrets; enrollment tokens are returned once, with revocable metadata retained.

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

Installation endpoints are GET `/api/runner-installation`, `/api/runner-package` and `/api/runner-package/checksum`; `?variant=offline` selects that package. Metadata reports fixed `version`, `source` (`github` or `server`), SHA256, compressed bytes, runtime/unpacked bytes and availability. Published Servers embed their Runner descriptor and advertise exact public GitHub URLs without staged packages. A complete matching local mirror takes precedence for installer links; it does not change the installer's fixed runtime identity or public default. Local package routes retain browser authentication. GET `/api/daemon/runtime/:sha256` uses enabled machine credentials, rejects browser cookies and serves an exact retained digest independently of current release metadata. Public GitHub requests carry no Camp credential. [Installation](install.md#build-and-publish-a-release) owns publishing and explicit mirror selection.

## Collaborations

Any enabled member can start a task, add inputs and answer an open question. Only requester/admin can cancel, continue, add allowance or refresh inactive participants. All task drafts/results remain team-visible.

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/rooms/:id/tasks` | `{requestId,goal,participantAgentIds,firstAgentId,threadRootId?,references?}` → `{task}` |
| GET | `/api/rooms/:id/tasks` | `{tasks}` |
| GET | `/api/tasks/:id` | `{task,runs}` |
| POST | `/api/tasks/:id/inputs` | `{requestId,content}` |
| POST | `/api/tasks/:id/answers` | `{requestId,revision,questionId,content}` |
| POST | `/api/tasks/:id/continue` | `{requestId,revision,agentId?,content?,addTurns?,addModelRequests?,fromLatest?,participantAgentIds?}` |
| POST | `/api/tasks/:id/cancel` | `{requestId,revision}` |

Operations return `{task}` and persist user/scope/request receipts. Identical replay precedes later revision/status checks; changed content conflicts. Answers are first-valid-submission wins. Initial allowance is **6 executing rounds and 24 model requests**; one explicit continuation can add at most 24 rounds and 96 requests. Waiting consumes neither. Effective per-Run limits remain in [model configuration](dsh.md#configuration-and-budgets).

States are `queued`, `running`, `waiting_for_person`, `needs_attention`, `publishing`, `completed`, `cancelling`, `cancelled`, `interrupted`. A supplied participant set on continuation explicitly refreshes Agent configurations/routes; ordinary continuation keeps the old grant. Each round freezes inputs/checkpoint. Intermediate Runs use `checksDeferred` and do not publish Room files. Final publication requires checks, cleanup, current authority and original shared-baseline CAS. Conflict retains the draft; `fromLatest` explicitly rebases work with the old draft as read-only evidence, without automatic merging. See [task lifecycle](architecture.md#collaboration-tasks).

## Daemon protocol v2

Machine routes reject Cookie/Origin headers and obey Host policy. Non-loopback connections require TLS; the Runner verifies Server certificates and same-origin URLs. No arbitrary upstream address, port tunnel or cross-machine Store is exposed.

| Method | Path | Contract |
| --- | --- | --- |
| POST | `/api/daemon/enroll` | `{token,registrationId,credential,name,storageId}` → `{runnerId}` |
| GET | `/api/daemon/connect` | WSS bearer; hello/welcome with protocol and epoch |
| GET | `/api/daemon/operations/:id/input?part=project\|references` | Exact authorized input digest/length |
| POST | `/api/daemon/operations/:id/finalizing` | `{}` → bounded upload authorization or terminal receipt |
| PUT | `/api/daemon/operations/:id/artifact` | Whole bounded tar with `X-Camp-SHA256` |
| POST | `/api/daemon/operations/:id/complete` | Restricted Result → `{disposition,runId?,status?,reason?}` |
| POST | `/api/daemon/model/:attempt/v1/messages` | Attempt capability in `x-api-key`; frozen Messages JSON/SSE |

Runner persists identity/credential before enrollment; identical registration replays after lost response, different content conflicts. Revocation prevents further machine access. Fixture execution is explicitly labelled and requires Server `--allow-fixture-runners`, while using the same journal/protocol lifecycle.

Frames are `hello`, `welcome`, `command`, `accepted`, `event`, `event_ack`, `heartbeat`, `grant`, `cancel`, `rejected`. Commands carry stable identities and deadlines. Execution needs durable acceptance and a grant. Renewal is independent of command hash, echoes its causal requestId and uses that request's monotonic send time. Old epochs cannot renew leases. Events are sequenced per operation and ACK only after commit. Reconnect reconciles original operations before new work.

Completion disposition is `accepted`, `committed` or `rejected`; upload acceptance is not publication. Completion identity/canonical manifest hash replays the original final receipt even after lease expiry. Changed content conflicts. Old Attempts, fences, expired authority, cancellation or invalid archives cannot publish. Model access is revoked before upload; corrupt final results seal the operation and release its slot. Uncertain cleanup suspends the Runner atomically with its receipt. Disabled machines can retrieve terminal receipts; revoked credentials cannot. These are idempotent state/publication guarantees, not exactly-once model calls or external effects.

Protocol v2 carries optional `ExecutionSpec.task` and `Result.intent`. One legal coordination decision must match its task, approved participants, call identity, paths and fence. A persisted intent alone cannot restart a prompt. Checkpoint, confirmed cleanup, terminal receipt and successor commit before later dispatch.
