-- +goose Up
CREATE TABLE work_items (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 room_id TEXT NOT NULL REFERENCES rooms(id), owner_user_id TEXT NOT NULL REFERENCES users(id),
 policy_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL CHECK(status IN ('open','running','waiting','waiting_for_person','needs_attention','completed','cancelled')), revision INTEGER NOT NULL CHECK(revision>0),
 value TEXT NOT NULL CHECK(json_valid(value)), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX work_items_room ON work_items(room_id,seq);
CREATE TABLE work_item_evidence (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 work_item_id TEXT NOT NULL REFERENCES work_items(id), event_id TEXT NOT NULL REFERENCES automation_events(id),
 kind TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(work_item_id,event_id)
);
CREATE TABLE work_item_decisions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 work_item_id TEXT NOT NULL REFERENCES work_items(id), value TEXT NOT NULL CHECK(json_valid(value))
);
CREATE INDEX work_item_decision_history ON work_item_decisions(work_item_id,seq);
CREATE TABLE work_item_tasks (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 work_item_id TEXT NOT NULL REFERENCES work_items(id), task_id TEXT NOT NULL UNIQUE REFERENCES collaboration_tasks(id),
 result_event_id TEXT NOT NULL DEFAULT '', reserved_requests INTEGER NOT NULL CHECK(reserved_requests>0),
 created_at TEXT NOT NULL, UNIQUE(work_item_id,task_id)
);
CREATE TABLE work_item_watches (
 work_item_id TEXT NOT NULL REFERENCES work_items(id), source_id TEXT NOT NULL, thread_key TEXT NOT NULL,
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)), PRIMARY KEY(work_item_id,source_id,thread_key)
);
CREATE INDEX work_item_source_watch ON work_item_watches(source_id,thread_key,enabled);
CREATE TABLE work_item_timers (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, work_item_id TEXT NOT NULL REFERENCES work_items(id), due_at TEXT NOT NULL,
 condition TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','fired','cancelled')),
 event_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE INDEX work_item_timer_queue ON work_item_timers(status,due_at,id);
CREATE TABLE work_item_actions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, work_item_id TEXT NOT NULL REFERENCES work_items(id),
 kind TEXT NOT NULL, status TEXT NOT NULL, task_id TEXT NOT NULL DEFAULT '',
 delivery_id TEXT NOT NULL DEFAULT '', body TEXT NOT NULL, created_at TEXT NOT NULL
);
CREATE TABLE work_item_requests (
 user_id TEXT NOT NULL REFERENCES users(id), request_id TEXT NOT NULL, request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)), cancelled INTEGER NOT NULL DEFAULT 0 CHECK(cancelled IN (0,1)),
 PRIMARY KEY(user_id,request_id)
);
CREATE TABLE work_inbox (
 id TEXT PRIMARY KEY NOT NULL, user_id TEXT NOT NULL REFERENCES users(id),
 kind TEXT NOT NULL CHECK(kind IN ('mention','run_finished','review_requested','review_updated','task_question','task_attention','task_finished','work_item_attention','work_item_finished')),
 room_id TEXT NOT NULL REFERENCES rooms(id), message_id TEXT NOT NULL REFERENCES messages(id),
 run_id TEXT NOT NULL DEFAULT '', review_id TEXT NOT NULL DEFAULT '', task_id TEXT NOT NULL DEFAULT '',
 work_item_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, read_at TEXT NOT NULL DEFAULT '',
 UNIQUE(user_id,kind,message_id)
);
INSERT INTO work_inbox(id,user_id,kind,room_id,message_id,run_id,review_id,task_id,created_at,read_at)
SELECT id,user_id,kind,room_id,message_id,run_id,review_id,task_id,created_at,read_at FROM inbox;
DROP TABLE inbox;
ALTER TABLE work_inbox RENAME TO inbox;
-- +goose StatementBegin
CREATE TRIGGER work_evidence_immutable_update BEFORE UPDATE ON work_item_evidence BEGIN SELECT RAISE(ABORT,'work evidence is immutable'); END;
CREATE TRIGGER work_evidence_immutable_delete BEFORE DELETE ON work_item_evidence BEGIN SELECT RAISE(ABORT,'work evidence is immutable'); END;
CREATE TRIGGER work_decision_immutable_update BEFORE UPDATE ON work_item_decisions BEGIN SELECT RAISE(ABORT,'work decisions are immutable'); END;
CREATE TRIGGER work_decision_immutable_delete BEFORE DELETE ON work_item_decisions BEGIN SELECT RAISE(ABORT,'work decisions are immutable'); END;
CREATE TRIGGER source_event_fact_immutable BEFORE UPDATE OF id,room_id,source_id,thread_key,kind,actor_id,actor_kind,message_id,external_event_id,value ON automation_events BEGIN SELECT RAISE(ABORT,'source event facts are immutable'); END;
CREATE TRIGGER source_event_delete_immutable BEFORE DELETE ON automation_events BEGIN SELECT RAISE(ABORT,'source event facts are immutable'); END;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
