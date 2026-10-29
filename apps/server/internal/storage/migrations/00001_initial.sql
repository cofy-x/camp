-- +goose Up
PRAGMA application_id = 1128353106;

CREATE TABLE resources (
 id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, source_path TEXT NOT NULL DEFAULT '',
 current_commit TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(environment)),
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE agents (
 id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL, handle TEXT NOT NULL UNIQUE COLLATE NOCASE,
 icon TEXT NOT NULL DEFAULT 'assistant', instructions TEXT NOT NULL DEFAULT '',
 access TEXT NOT NULL CHECK(access IN ('read','write')), enabled BOOLEAN NOT NULL CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL CHECK(revision > 0), created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE rooms (
 id TEXT PRIMARY KEY NOT NULL, name TEXT NOT NULL UNIQUE COLLATE NOCASE, description TEXT NOT NULL DEFAULT '',
 resource_id TEXT NOT NULL REFERENCES resources(id), default_agent_id TEXT REFERENCES agents(id),
 activity_seq INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE memberships (
 room_id TEXT NOT NULL REFERENCES rooms(id), agent_id TEXT NOT NULL REFERENCES agents(id),
 created_at TEXT NOT NULL, PRIMARY KEY(room_id,agent_id)
);
CREATE TABLE messages (
 id TEXT PRIMARY KEY NOT NULL, room_id TEXT NOT NULL REFERENCES rooms(id), thread_root_id TEXT REFERENCES messages(id),
 seq INTEGER NOT NULL CHECK(seq > 0), author_kind TEXT NOT NULL CHECK(author_kind IN ('user','agent','system')),
 agent_id TEXT REFERENCES agents(id), run_id TEXT NOT NULL DEFAULT '', content TEXT NOT NULL,
 request_id TEXT NOT NULL DEFAULT '', fingerprint TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(room_id,seq)
);
CREATE UNIQUE INDEX message_request ON messages(room_id,request_id) WHERE request_id <> '';
CREATE UNIQUE INDEX message_run ON messages(run_id,author_kind) WHERE run_id <> '';
CREATE INDEX message_thread ON messages(room_id,thread_root_id,seq);
CREATE TABLE runs (
 id TEXT PRIMARY KEY NOT NULL, room_id TEXT NOT NULL REFERENCES rooms(id), thread_root_id TEXT REFERENCES messages(id),
 origin_message_id TEXT NOT NULL REFERENCES messages(id), agent_id TEXT NOT NULL REFERENCES agents(id),
 resource_id TEXT NOT NULL REFERENCES resources(id), status TEXT NOT NULL CHECK(status IN ('queued','preparing','running','checking','cancelling','succeeded','failed','cancelled','interrupted')),
 snapshot TEXT NOT NULL CHECK(json_valid(snapshot)), created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
 UNIQUE(origin_message_id,agent_id)
);
CREATE INDEX runs_queue ON runs(status,created_at,id);
CREATE INDEX runs_room ON runs(room_id,created_at,id);
CREATE UNIQUE INDEX runs_agent_active ON runs(agent_id) WHERE status IN ('preparing','running','checking','cancelling');
CREATE TABLE run_events (
 run_id TEXT NOT NULL REFERENCES runs(id), seq INTEGER NOT NULL, type TEXT NOT NULL, text TEXT NOT NULL,
 created_at TEXT NOT NULL, PRIMARY KEY(run_id,seq)
);
CREATE TABLE agent_conversations (
 id TEXT PRIMARY KEY NOT NULL, agent_id TEXT NOT NULL REFERENCES agents(id), room_id TEXT NOT NULL REFERENCES rooms(id),
 thread_root_id TEXT NOT NULL DEFAULT '', resource_id TEXT NOT NULL REFERENCES resources(id), agent_revision INTEGER NOT NULL,
 runtime_key TEXT NOT NULL, generation INTEGER NOT NULL, session_id TEXT NOT NULL, context_seq INTEGER NOT NULL DEFAULT 0,
 resumable BOOLEAN NOT NULL DEFAULT FALSE, UNIQUE(agent_id,room_id,thread_root_id)
);
CREATE TABLE workspace_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, room_id TEXT NOT NULL DEFAULT '', entity_id TEXT NOT NULL,
 created_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE workspace_events;
DROP TABLE agent_conversations;
DROP TABLE run_events;
DROP TABLE runs;
DROP TABLE messages;
DROP TABLE memberships;
DROP TABLE rooms;
DROP TABLE agents;
DROP TABLE resources;
PRAGMA application_id = 0;
