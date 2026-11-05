# Feishu observations

A Room can receive encrypted Feishu event webhooks. Intake preserves a signed original and immutable normalized evidence before any assessment. External app, tenant, chat and sender identities remain separate from Camp members. Merely connecting or reading a source starts no Agent work.

## Connect a chat

1. As an administrator, open the Room's **Automation** settings and add a Feishu source with the application's App ID, tenant key and chat ID. Each source is permanently scoped to that app, tenant and chat.
2. Supply the application's Verification Token and Encrypt Key from Feishu's **Events & Callbacks → Encryption Strategy** settings. Camp requires both credentials and accepts encrypted webhook delivery only.
3. Configure the application's event request URL as `/api/hooks/feishu/<source-id>` on an externally reachable HTTPS Camp origin. Subscribe to **Receive message** (`im.message.receive_v1`, schema `2.0`). Camp answers an encrypted URL verification challenge after checking its token.
4. Grant the relevant Feishu message subscription permissions and publish or enable the application through Feishu's own administration controls. Camp does not configure the application, expose a tunnel or start a service.

Use the [official event reception guide](https://open.feishu.cn/document/server-docs/event-subscription-guide/event-subscription-configure-/encrypt-key-encryption-configuration-case) and [receive-message event contract](https://open.feishu.cn/document/server-docs/im-v1/message/events/receive) for Feishu-side configuration. Loopback HTTP is supported for local protocol checks; remote ingress requires HTTPS.

Only text messages from human senders become assessment candidates. Other message types, bots and unsupported event types remain recorded as ignored observations. Message content remains authored, including whitespace, and is bounded at 64 KiB. Encrypted request bodies are limited to 1 MiB. Messages from a different app, tenant or configured chat are rejected. Camp does not poll missed Feishu history or download attachments.

## Delegate and follow

An administrator can configure the source's existing automation policy. Policies start disabled. **Observe** records a bounded assessment; **Auto** can authorize a finite CollaborationTask under the approved participants, current permissions and task/daily allowances. Each policy save establishes a new event frontier and does not execute earlier observations. See [event automation](architecture.md#event-automation).

Allowed Feishu sender IDs use `senderTenantKey:openId`. The sender tenant may differ from the receiving application's tenant; preserve both identities when configuring the allowlist. These IDs never imply Camp membership. Source conversation keys use receiving tenant, chat and root-message identity. A conversation can feed several active collaborations; routing supplies context under their existing grants.

Feishu ingress grants no outbound Feishu API access. External answers and comments are unavailable for this adapter; task questions, results and requests for judgment remain visible in Camp. Finite execution, Work Item decisions and Inbox notifications retain their existing owners.

## Recovery and private state

Camp verifies ordinary delivery signatures over the timestamp, nonce, Encrypt Key and exact request bytes before decryption. It also checks the Verification Token inside the encrypted payload. URL verification may omit signature headers but must pass encrypted-token authentication. Browser cookies, Origin and Authorization headers are rejected on webhook ingress.

The source-scoped Feishu event ID identifies its durable delivery receipt; changed content under the same ID conflicts. Re-encryption or a new delivery ID does not duplicate the same recorded content. Original encrypted bytes, normalized evidence, source revision, private credential reference and receipt commit with the canonical event and subscribed inputs in one transaction. Acknowledgement follows that commit. Disabling a source or rotating credentials also rejects an in-flight intake using the old credential.

Private immutable token/key pairs live under `configuration/feishu-webhooks` in the Server data directory with owner-only permissions. Team SQLite stores opaque references and request hashes, never the credential values or decrypted webhook header. Downloaded originals remain encrypted; the normalized event exposes the message text and external identities. Back up the complete stopped Server data directory to preserve both old encrypted originals and their key versions.

Configuration uses actor-scoped request IDs and expected revisions. After an uncertain write, read the saved receipt. If no receipt exists, cancel that unconfirmed operation to fence a delayed write before starting another request. Browser recovery retains the operation ID, never the Verification Token or Encrypt Key. The [Feishu API](api.md#feishu-observation) owns the wire protocol.
