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

## From discussion to delivery

1. **Discuss in a Room.** With Automation off, ordinary messages start no Agent work. Select an Agent or mention its handle for one execution; **Post only** also suppresses automated assessment and context tracking.
2. **Collect sources.** Attach files or select Library sources. Uploads retain the original. **Add to Message** prepares an unsent reference to an exact version. Use Markdown for readable documents. UTF-8 files can be viewed as source; other attachments remain downloadable as originals.
3. **Send work explicitly.** Read the result and exact saved files in the conversation. Check status describes configured commands, not business acceptance. **Ask agent…** prepares a cited follow-up that still needs a recipient and Send.
4. **Edit and review.** Library edits autosave privately; **Publish Changes** creates a shared version. Open a shared file and use **Request Review…** to ask a person. Requested changes, resubmission and approval use the same review Thread.
5. **Deliver a version.** Deliver the approved current version explicitly. Delivered bytes remain pinned after later edits. History and restore preserve earlier versions.

File uploads and limits are described in the [upload contract](api.md#file-uploads); human decisions follow the [review and delivery contract](api.md#reviews-and-delivery).

## Work with Agents

Open **Work** on the desktop, a Room's **Work** shortcut, or choose **Follow up…** on a message. Record the goal for later or explicitly delegate it to start work. The source message is retained as evidence; following later messages is an explicit choice. Before execution, confirm the participants, scope and finite allowance. Agents can hand off, ask a person and finish. Work continues after the browser closes.

Collaboration uses a separate team-visible draft. Final publication compares the starting shared version; a conflict retains the draft and requires explicit continuation. Completing Agent work, publishing files, human approval and delivery remain distinct actions.

## Follow a matter

Open **Work** to see your unfinished matters across Rooms. **Needs me** shows your questions and work requiring recovery; ordinary waiting and execution remain in **My work**. **Completed** holds finished matters, while **All work** includes team-visible matters. The Room filter narrows any view. A Room's **Work** shortcut opens that same application filtered to the Room. **Discussion** stays in Workspace, and **Files** opens Library. Each matter keeps its current question, progress and exact-version result together. Execution rounds and their audit records remain available inside the matter, with a direct return from task and Run inspection. **Open Room** returns to its discussion. Inbox reminders open the same matter; reading a reminder does not complete it. Earlier independent collaborations remain available in execution history.

**Overview** shows what needs your attention and the latest result. **Activity** browses one kind of record at a time. **Manage** holds reminders, context, automatic follow-up and allowance settings. Switching views retains an unfinished answer; reading records starts no Agent work.

**Result ready** means the latest execution produced a result you can open. It does not itself approve a document or confirm that the matter is complete; formal review remains an explicit choice.

Describe the goal. Completion criteria and a business due date are optional. A matter without criteria needs human confirmation before completion. Authorization has its own finite expiry, separately from the business due date. Detailed ceilings and policy settings are available when needed; changing a due date does not authorize work.

Answer the current question directly, read the result or stop following the matter. An answer to an executing task's question continues within that task's remaining grant. Starting another execution requires an explicit finite authorization. Stopping a matter prevents further follow-ups and cancels its current execution; the stopping state remains visible until cancellation settles. A lost response preserves the submitted request for recovery before another action.

Waiting can have a follow-up time; a due timer produces an event rather than executing directly. An existing Auto policy can judge follow-ups under its current delegation; otherwise they appear in Inbox for the owner. Task completion adds evidence and does not itself close the matter. Exhausted cumulative allowances require explicit authorization.

Use **Manage → Reminder** to schedule an Inbox notification at a future time. The optional waiting condition provides context; it does not schedule a notification by itself.

The Room's management menu holds source connections, automatic processing rules, shared notes and activity records. **Remember this…** prepares a shared note with the original message as provenance; saving it remains explicit. Notes have version history and are independent of any Agent. Configure [GitHub observations](github.md) here. Feishu connection is deferred and is not offered in the ordinary workflow. New source policies remain off until an administrator explicitly saves an Observe or Auto delegation. External messages, retrieved memory and Agent proposals cannot change permissions or allowances.

## Visibility and access

All Rooms, shared files and collaboration task drafts are visible to the team. Task drafts remain separate from shared Room files until final publication. Personal editing drafts, Inbox, reads and desktop layouts belong to each member.

Sharing an Agent lets teammates invoke it. Machine administration and source import still require owner/admin authority. Use **Set Up Agent Work…** in an empty Room or **Help → Getting Ready** for model setup, Runner connection and an explicitly requested first task. Registration and self-test success do not establish real-model success.
