# Platinum visual and interaction design

Camp uses compact gray controls, crisp bevels, striped active title bars, shared icons and overlapping windows, initially informed by [Platinum9](https://github.com/grassmunk/Platinum9). Readability, Unicode and usable keyboard/touch behavior guide adaptations. Change shared controls and tokens together when the visual direction evolves.

## Sources of truth

Exact dimensions, colors, breakpoints and interaction values live in code:

| Source | Owns |
| --- | --- |
| [Platinum styles](../packages/platinum/src/styles.css) | Tokens, frames, controls and responsive chrome |
| [Typography](../packages/platinum/src/typography.css) / [font licenses](../packages/platinum/src/fonts/README.md) | Bundled Chicago Kare/Inter and Unicode fallbacks |
| [Controls](../packages/platinum/src/controls.tsx), [Select](../packages/platinum/src/select.tsx), [overlays](../packages/platinum/src/overlays.tsx), [Palette](../packages/platinum/src/palette.tsx) | Reusable semantics, focus and dismissal |
| [Window frame](../packages/platinum/src/desktop/WindowFrame.tsx) | Activation, controls, shading and content bounds |
| [Application styles](../apps/dashboard/src/shared/styles/apps.css) | Toolbars, aligned action groups, status and errors |

Use shared controls and generic icons. Put feature-specific styling with its feature; shared primitives belong in Platinum with a Gallery example. Chicago Kare labels chrome, Inter labels body text, fields and documents. Keep Unicode fallbacks; avoid synthetic bold, forced bitmap text or shrinking text to conceal overflow. Gray raised/inset surfaces establish hierarchy; selection, focus and status use meaningful color plus a visible non-color cue.

## Content and information hierarchy

Lead with current facts and the next useful action. Use grouping, alignment and spacing to make relationships and actions clear. Add secondary labels and descriptions only when they provide information needed to choose, act or recover. Each sentence should support a decision, input or recovery step.

| Information | Placement |
| --- | --- |
| Current value, destination, recipients or work state | Beside its control/item |
| Required input, blocking capability, error and recovery | At the affected action/field |
| Shared publication, revocation or other decision-changing consequence | At the action or confirmation |
| Source explaining an externally locked field | Short label beside that field |
| Runtime provenance, detailed limits and previous observations | Named disclosure near the group |
| Repeated heading, action label or already visible status | Remove |

Keep labels concrete and vocabulary consistent. Empty states offer visible, editable starting points and enough explanation to choose; populated panels should not retain that tutorial. Tooltips supplement labels, never carry a required permission, consequence or recovery. Blocking errors remain outside disclosures.

Distinguish configured from verified, private draft from shared version, captured from published, checks from tool activity, and completed work from human approval. A request allowance is not a billing cap. Simplification must preserve these facts and the user's unsaved input. Technical names belong in diagnostics when they do not help a product decision.

An ellipsis means further input/confirmation is required. **Ask agent…** prepares a cited draft requiring recipient selection and Send. Overflow menus use a concise accessible name, visible focus, ArrowDown opening and Escape focus return.

## Windows, controls and narrow layouts

Use one title frame, optional compact toolbar, remaining content area and optional status bar. Status wraps without clipping save errors. Active/inactive, pressed, selected, focused, default, disabled and error states have distinct shared cues.

The menu bar follows the active window. File owns generic desktop selection/open commands and names the selected app; Window/overview recover hidden windows. Dialogs contain focus and restore it on dismissal. Shared nonmodal Palettes keep anchoring and dismissal with the host: Escape/close restores toggle focus; outside dismissal preserves the destination's focus. Inbox uses an aligned unread column and a stable pressed toggle; mark an item read only after draft-safe navigation succeeds.

Preserve text selection, copy/paste, editing shortcuts and IME composition. Shading/hiding retains drafts and live work. Use the shared menu, Select, tabs, Radio and Progress rather than imitating them with feature-specific markup. Native wheel/touch scrolling remains usable even where host scrollbar artwork differs. Menus and dialogs stay inside visible bounds.

Adapt to application-window width through wrapping, stacking and scrolling. Narrow Settings/Agents/Runners replace sidebars with the shared pop-up. Narrow Workspace uses one Room identity/switcher with unread indicators and a separate action row. Long paths/titles wrap without widening the window. Narrow desktop mode keeps active-app identity and access to inactive windows.

Mouse and touch use the same compact visual control dimensions. Control Strip transparent hit regions may expand independently; they must not cover a window. Keep safe-area handling, close/return/save actions and keyboard focus reachable. Radio arrow keys move and select within the same native group, wrapping at both ends and skipping unavailable choices; Tab leaves the group. Avoid page-wide horizontal overflow. Wide document tables may have their own labelled, keyboard-accessible horizontal scroll region.

## Conversation and results

Use the [bundled pixel avatars](../apps/server/internal/avatars/catalog.json) consistently in conversations, mentions, member lists and Agent views. Members use animal silhouettes and Agents use machine silhouettes; visible names and accessible Agent identity remain present. Keep avatars visible in narrow conversations. The catalog's original artwork shares Camp's MIT license, loads from the Server and needs no external avatar service. Catalog IDs and their artwork are permanent; add an ID for a new design. Current profiles supply the conversation avatar without rewriting historical attribution or frozen execution inputs. Avatar choices use the shared Radio control with a visible selection and focus outline.

The composer names selected plus mentioned Agents once. **Post only** uses a discussion placeholder and keeps its no-execution consequence visible. Empty-Room starters prepare editable drafts, preserve existing text and never send. Upload, recipient and preview lists scroll within bounds so message input and Send remain visible in short windows. Preview buttons inside forms cannot submit them.

Upload/preparation completion belongs to its original Room/Thread and never sends automatically. Collapsed PDF/DOCX cards still prepare text; pending preparation blocks Agent submission while Post remains possible. Show actual source limits when relevant, with original download in every state. Physical PDF pages and nonempty DOCX paragraphs are honest source locations, not rendered Word pages. Citation labels come from frozen supplied evidence; a citation does not prove a claim.

Completed results lead with publication/capture/check facts, then exact-version files, then output. Errors remain visible. Deletion-only results retain changes and patch access. A file list does not prove successful capture; interrupted or partial check evidence cannot show Passed or remain Running after termination. Conversation and inspector use the same record interpretation.

Short replies stay inline; long replies disclose the full original Markdown and citations. Keep Markdown controls mounted across desktop updates so focus, text selection and an in-progress pointer click survive. The first paragraph can be an earlier intention, so do not synthesize a conclusion from it. Remember expansion per message/account browser session; collapse restores focus. Follow-up references retain the complete message. Reading, previewing and downloading start no work and do not steal later reading position. Ask agent preserves Room versus existing Thread scope; Reply in thread explicitly creates that scope.

## Library and human review

Library is the sole browser/editor. Group Resource commands separately from file commands and message handoff. New Document/History stay visible; More contains Refresh and administrator Environment. Sources uses a scrolling inset directory and a separate settings footer. On narrow windows it becomes a drawer; opening focuses selection, Escape/Done returns toggle focus and Tab exit preserves the intended focus destination. Preview/Reviews tabs retain reading state. Give normal documents one vertical reading scroll owner; short windows can scroll the whole file pane to retain actions.

Safe Markdown supports GFM tables and read-only task checkboxes. Keep raw HTML disabled and remote images as explicit links. Source text and exact download remain available. Body headings keep the document font, distinct from chrome.

Editor autosave is private. Explicit publication uses its frozen baseline; delayed responses cannot mark newer text published. Navigation, close and sign-out flush drafts; failure preserves local recovery. Private conflicts compare drafts before replacement; shared conflicts show latest bytes before explicit baseline change. History opens exact old versions without restoring; a historical banner offers Return to Latest. Restore requires confirmation and creates another shared version. Technical patch export belongs in comparison details.

Review forms freeze their file while open and survive background refresh. Uncertain requests keep the exact body and offer receipt recovery before edits. Comments autosave privately and stay recoverable after another session decides. Put the submitted document before decision controls; requester management belongs under Manage Review. A committed user decision focuses its resulting state; background updates preserve position/focus. Resolved feedback and decisions remain in history. Delivered work leads with its immutable document; an outdated review separately offers current files. Ask agent prepares a cited draft and never starts work by itself. Formal approval/delivery always remains a human action.

## Readiness and Runners

An empty Room with unavailable execution gives administrators **Set Up Agent Work…**; ordinary teammates retain the browser discussion path. Getting Ready leads with one next action: configure a model, connect a Linux Runner, choose a Runner for an explicitly requested sample task, or start working after recorded success. Navigation starts no model request or enrollment. Connection navigation opens the existing persistent Connect Runner view; returning to machine overview keeps its unfinished connection draft. Detailed observations remain independently visible and never become a substitute for first-task evidence.

Getting Ready separates model configuration, observed provider responses, Runner self-test reports and recorded native task success. Opening and Refresh Status read the current aggregate without probing dependencies, invoking a model or pausing admission. Preserve the actual observation timestamps; prior observations survive failures and older responses cannot replace newer reports. Optional document readers belong to document preparation, independently of discussion/plain-text work. Model, environment and storage panels load independently. An available runtime or previous connection cannot certify a current successful task. See [readiness ownership](architecture.md#readiness-reporting).

Runners uses a selected-machine list/detail panel: Overview for availability and repair, Access for defaults/grants, Diagnostics for versions/timestamps/history. Agent choice stays in conversation; machine choice stays in Agents/Runners. A submitted request keeps its Runner and explains offline/busy waits.

Connect Runner is a separate persistent view: download, private code, setup and first task. Show installer plus runtime transfer and an offline option. Preserve unfinished input and explain expiry/recovery. First-task success needs actual native work, separately from registration, connectivity and installation self-test. Destructive machine actions confirm their consequences.

Collaboration start shows goal, participants, first Agent, finite allowance and unpublished draft before submission. Task detail leads with recorded state and version-specific files. Overview, Activity and Task setup separate result, turns and authorization; questions/errors/continuation stay visible outside tabs. Late inputs apply to the next round. Work completed does not establish approval/delivery.

## Review checklist

Choose scenes affected by the change; shared desktop changes need broader coverage. Record viewport, browser, device scale, content, window bounds and interaction state for reproducible comparisons. UI pull requests embed before/after screenshots directly as GitHub image attachments so reviewers can see the comparison without downloading an archive. CI artifacts retain supporting traces and reports. Keep generated screenshots/reports ignored.

| Scene | Inspect |
| --- | --- |
| Overlapping windows and menus | Active/inactive frames, layering, icons, menu bar and Control Strip |
| Controls/dialog/Palette | Pressed, selected, focus/default/disabled states, keyboard opening and dismissal |
| Changed app | Normal, loading/empty, long names, failed/uncertain writes and recovery |
| Narrow/short/resized windows | Reachable composer/actions, bounded scroll, no page overflow, focus-safe navigation |
| DPR 1, fractional (e.g. 1.5), DPR 2 | Text, borders, title stripes, icons and Unicode fallbacks |

Typical review sizes are 1440 × 900 desktop and 390 × 844 touch; include smaller app bounds and zoom where relevant. Use actual keyboard, pointer and touch actions alongside screenshots: switch, resize/shade, open/dismiss, edit and recover. Compare copy as well as geometry, including a failed state when reducing detail. Use [README checks](../README.md#checks-and-maintenance); visual fixtures, actual protocol tests, native isolation and real model runs establish different evidence.
