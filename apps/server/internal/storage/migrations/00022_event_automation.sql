-- +goose Up
CREATE TABLE automation_policies (
 id TEXT PRIMARY KEY, room_id TEXT NOT NULL REFERENCES rooms(id),
 source_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
 owner_user_id TEXT NOT NULL REFERENCES users(id), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 mode TEXT NOT NULL CHECK(mode IN ('observe','auto')), value TEXT NOT NULL CHECK(json_valid(value)),
 credential_ref TEXT NOT NULL DEFAULT '', start_after INTEGER NOT NULL DEFAULT 0,
 UNIQUE(room_id,source_id)
);
CREATE TABLE automation_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 room_id TEXT NOT NULL REFERENCES rooms(id), source_id TEXT NOT NULL, thread_key TEXT NOT NULL,
 kind TEXT NOT NULL, actor_id TEXT NOT NULL, actor_kind TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '', external_event_id TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL, value TEXT NOT NULL CHECK(json_valid(value)),
 evaluation_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '', policy_revision INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX automation_room_events ON automation_events(room_id,seq);
CREATE INDEX automation_pending_events ON automation_events(status,seq);
CREATE TABLE automation_evaluations (
 id TEXT PRIMARY KEY, source_id TEXT NOT NULL, thread_key TEXT NOT NULL,
 status TEXT NOT NULL, candidate TEXT NOT NULL CHECK(json_valid(candidate)),
 assessment TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(assessment)), created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX automation_thread_evaluation ON automation_evaluations(source_id,thread_key) WHERE status='running';
CREATE TABLE automation_usage (
 policy_id TEXT NOT NULL REFERENCES automation_policies(id), day TEXT NOT NULL,
 evaluations INTEGER NOT NULL DEFAULT 0, tasks INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(policy_id,day)
);
CREATE TABLE automation_subscriptions (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES collaboration_tasks(id),
 source_id TEXT NOT NULL, thread_key TEXT NOT NULL, enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision>0), closed_after INTEGER NOT NULL DEFAULT 0,
 UNIQUE(task_id,source_id,thread_key)
);
CREATE INDEX automation_thread_subscriptions ON automation_subscriptions(source_id,thread_key,enabled);
CREATE TABLE automation_requests (
 user_id TEXT NOT NULL REFERENCES users(id), request_id TEXT NOT NULL, request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)), PRIMARY KEY(user_id,request_id)
);
CREATE TABLE automation_deliveries (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES collaboration_tasks(id),
 source_id TEXT NOT NULL, policy_id TEXT NOT NULL REFERENCES automation_policies(id),
 revision INTEGER NOT NULL CHECK(revision>0), status TEXT NOT NULL,
 value TEXT NOT NULL CHECK(json_valid(value)), credential_ref TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE INDEX automation_delivery_queue ON automation_deliveries(status,created_at,id);

-- +goose Down
-- Preserve event originals, delegations, inputs and uncertain delivery receipts.
SELECT 1;
