# Team workflow

[Install Camp](install.md) to create a team, or join an existing Server with a browser address and invitation. Discussion, Markdown and plain-text editing work without a model or Runner. Configure a model and connect a supported Runner when you want Agents to execute work.

## Find your way around

Camp's browser desktop has six applications:

| Application | Use it for |
| --- | --- |
| Workspace | Room discussion and source context |
| Work | Personal matters across Rooms, questions and results |
| Library | Original sources, shared files, private text drafts and history |
| Agents | Responsibilities, file access, Runner targets and Room sharing |
| Runners | Connecting machines, execution readiness and access grants |
| Settings | Team, invitations, model configuration, storage and appearance |

## From discussion to results

1. **Discuss in a Room.** With Automation off, ordinary messages start no Agent work. Select an Agent or mention its handle for one execution; **Post only** also suppresses automated assessment and context tracking.
2. **Collect sources.** Attach files or select Library sources. Uploads retain the original. **Add to Message** prepares an unsent reference to an exact version. Use Markdown for readable documents. UTF-8 files can be viewed as source; other attachments remain downloadable as originals.
3. **Send work explicitly.** Read the result and exact saved files in the conversation. Check status describes configured commands, not business acceptance. **Ask agent…** prepares a cited follow-up that still needs a recipient and Send.
4. **Read or edit the result.** Work keeps the current outcome and exact saved files together. Library opens directly for reading. Edits autosave privately; **Publish Changes** creates a shared version. History and restore preserve earlier versions.

When a published file needs human approval, start a review from the file in Work or from **More → Human approval → Request Review…** in Library. Choose a reviewer for that exact saved version. Work shows the file's review status and opens the same review for the next action. The assigned reviewer can approve or request changes. After approval, the requester or an administrator can record delivery of the approved version. Find current review responsibilities in **Needs me** and previous decisions and deliveries through **Review history…** or the review discussion. Recorded document delivery fixes the approved version for the team.

Use **More → Add to Message…** and choose a Room to prepare a reference without sending. Original sources also offer **Create Working Copy…** with a Room choice. These commands retain the selected exact version while the menu is open.

File uploads and limits are described in the [upload contract](api.md#file-uploads); human decisions follow the [review and delivery contract](api.md#reviews-and-delivery).

## Work with Agents

Open **Work** on the desktop, a Room's **Work** shortcut, or choose **Follow up…** on a message. Record the goal for later or explicitly delegate it to start work. The source message is retained as evidence; following later messages is an explicit choice. Before execution, confirm the participants, scope and finite allowance. Agents can hand off, ask a person and finish. Work continues after the browser closes.

Collaboration uses a separate team-visible draft. Final publication compares the starting shared version; a conflict retains the draft and requires explicit continuation. Completing Agent work, publishing files, human approval and delivery remain distinct actions.

## Follow a matter

Open **Work** to see your unfinished matters across Rooms. **Needs me** shows your questions and work requiring recovery; ordinary waiting and execution remain in **My work**. **Completed** holds finished matters, while **All work** includes team-visible matters. The Room filter narrows any view. A Room's **Work** shortcut opens that same application filtered to the Room. **Discussion** stays in Workspace, and **Files** opens Library. Each matter keeps its current question, progress and exact-version result together. Execution rounds and their audit records remain available inside the matter, with a direct return from task and Run inspection. **Open Room** returns to its discussion. Inbox reminders open the same matter; reading a reminder does not complete it. Earlier independent collaborations remain available in execution history.

**Activity** opens the progress timeline. **More** contains reminders, context, automatic follow-up, allowance settings and detailed records. Switching views retains an unfinished answer. Reading records starts no Agent work.

**Result ready** means the latest execution produced a result you can open. A file review can continue after the matter has completed. An earlier file result opens its exact saved version. A new review request uses the current shared version, and a delivered review continues to point to the version it approved.

Describe the goal. New Work defaults to **When the goal is met**. Its authorized execution evaluates the saved goal or any more specific completion criteria you supplied. Camp applies the judgment after accepting the result and confirming any required effects. Choose **I'll confirm** in **Options** to decide when the matter is complete yourself. After either kind of acceptance, **Awaiting delivery** means Camp is waiting for the required external receipts. Open **View delivery records** to inspect pending comments. GitHub code deliveries show their current step directly. The last confirmed receipt completes the matter automatically if the acceptance is still current. Personal Room defaults can propose a different choice, and saved work keeps its chosen mode until you change it. A business due date and the finite authorization expiry are separate settings. Detailed ceilings and policy settings are available when needed.

Answer the current question directly, read the result or stop following the matter. An answer to an executing task's question continues within that task's remaining grant. Starting another execution requires an explicit finite authorization. Stopping a matter prevents further follow-ups and cancels its current execution; the stopping state remains visible until cancellation settles. A lost response preserves the submitted request for recovery before another action.

Waiting can have a follow-up time. A due timer records an event. An existing Auto policy can judge follow-ups under its current delegation. Otherwise, the owner receives a reminder in Inbox. Exhausted cumulative allowances require explicit authorization.

Use **More → Set reminder…** to schedule a future Inbox notification. A matter with a linked rule offers **Schedule follow-up…** in the same menu. The optional waiting condition provides context for the reminder or follow-up.

The Room's management menu holds source connections, automatic processing rules, shared notes and activity records. **Remember this…** prepares a shared note with the original message as provenance; saving it remains explicit. Notes have version history and are independent of any Agent. Configure [GitHub observations](github.md) here. Feishu connection is deferred and is not offered in the ordinary workflow. New source policies remain off until an administrator explicitly saves an Observe or Auto delegation. External messages, retrieved memory and Agent proposals cannot change permissions or allowances.

## Visibility and access

All Rooms, shared files and collaboration task drafts are visible to the team. Task drafts remain separate from shared Room files until final publication. Personal editing drafts, Inbox, reads and desktop layouts belong to each member.

Sharing an Agent lets teammates invoke it. Machine administration and source import still require owner/admin authority. Use **Set Up Agent Work…** in an empty Room or **Help → Getting Ready** for model setup, Runner connection and an explicitly requested first task. Registration and self-test success do not establish real-model success.
