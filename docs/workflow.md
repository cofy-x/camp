# Team workflow

[Install Camp](install.md) to create a team, or join an existing Server with a browser address and invitation. Discussion and plain-text editing work without a model or Runner. Configure a model and connect a supported Runner when you want Agents to execute work.

## Find your way around

Camp's browser desktop has five applications:

| Application | Use it for |
| --- | --- |
| Workspace | Rooms, Threads, Agent requests, collaborations and results |
| Library | Original sources, shared files, private text drafts and history |
| Agents | Responsibilities, file access, Runner targets and Room sharing |
| Runners | Connecting machines, execution readiness and access grants |
| Settings | Team, invitations, model configuration, storage and appearance |

## From discussion to delivery

1. **Discuss in a Room.** Ordinary messages start no Agent work. Select an Agent or mention its handle for one execution; **Post only** suppresses Agent execution.
2. **Collect sources.** Attach files or select Library sources. Uploads retain the original. **Add to Message** prepares an unsent reference to an exact version. PDF/DOCX preparation uses no model call; when text preparation is unavailable, the original remains downloadable. Reader availability depends on the [Server platform](install.md#supported-deployment).
3. **Send work explicitly.** Read the result and exact saved files in the conversation. Check status describes configured commands, not business acceptance. **Ask agent…** prepares a cited follow-up that still needs a recipient and Send.
4. **Edit and review.** Library edits autosave privately; **Publish Changes** creates a shared version. Open a shared file and use **Request Review…** to ask a person. Requested changes, resubmission and approval use the same review Thread.
5. **Deliver a version.** Deliver the approved current version explicitly. Delivered bytes remain pinned after later edits. History and restore preserve earlier versions.

File preparation and upload limits are described in the [file/document contract](api.md#uploads-and-documents); human decisions follow the [review and delivery contract](api.md#reviews-and-delivery).

## Collaborate with Agents

**Start collaboration** authorizes a goal, participants and a finite execution allowance. Agents can hand off, ask a person and finish. Work continues after the browser closes.

Collaboration uses a separate team-visible draft. Final publication compares the starting shared version; a conflict retains the draft and requires explicit continuation. Completing Agent work, publishing files, human approval and delivery remain distinct actions.

## Visibility and access

All Rooms, shared files and collaboration task drafts are visible to the team. Task drafts remain separate from shared Room files until final publication. Personal editing drafts, Inbox, reads and desktop layouts belong to each member.

Sharing an Agent lets teammates invoke it. Machine administration and source import still require owner/admin authority. Use **Set Up Agent Work…** in an empty Room or **Help → Getting Ready** for model setup, Runner connection and an explicitly requested first task. Registration and self-test success do not establish real-model success.
