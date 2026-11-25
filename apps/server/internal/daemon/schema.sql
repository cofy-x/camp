CREATE TABLE IF NOT EXISTS commands (
 id TEXT PRIMARY KEY,
 operation_id TEXT NOT NULL UNIQUE,
 digest TEXT NOT NULL,
 body BLOB NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('accepted','running','uploading','terminal')),
 result BLOB,
 archive_path TEXT NOT NULL DEFAULT '',
 cancelled INTEGER NOT NULL DEFAULT 0,
 receipt BLOB,
 created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS events (
 operation_id TEXT NOT NULL REFERENCES commands(operation_id),
 sequence INTEGER NOT NULL,
 kind TEXT NOT NULL,
 text TEXT NOT NULL,
 acknowledged INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(operation_id,sequence)
);
CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY,value TEXT NOT NULL);

CREATE TABLE IF NOT EXISTS coordination_intents (
 operation_id TEXT PRIMARY KEY REFERENCES commands(operation_id),
 body BLOB NOT NULL
);

CREATE TABLE IF NOT EXISTS event_usage (
 operation_id TEXT PRIMARY KEY REFERENCES commands(operation_id),
 event_count INTEGER NOT NULL,
 text_bytes INTEGER NOT NULL
);
INSERT INTO event_usage(operation_id,event_count,text_bytes)
SELECT operation_id,COUNT(*),SUM(length(CAST(text AS BLOB))) FROM events
WHERE NOT EXISTS (SELECT 1 FROM metadata WHERE key='event-usage-v1')
GROUP BY operation_id;
INSERT OR IGNORE INTO metadata(key,value) VALUES('event-usage-v1','1');
CREATE TRIGGER IF NOT EXISTS count_journal_event AFTER INSERT ON events
BEGIN
 INSERT INTO event_usage(operation_id,event_count,text_bytes)
 VALUES(NEW.operation_id,1,length(CAST(NEW.text AS BLOB)))
 ON CONFLICT(operation_id) DO UPDATE SET
  event_count=event_usage.event_count+1,
  text_bytes=event_usage.text_bytes+excluded.text_bytes;
END;
CREATE TRIGGER IF NOT EXISTS retire_event_usage AFTER UPDATE OF state ON commands
WHEN NEW.state='terminal' AND OLD.state<>'terminal'
BEGIN
 DELETE FROM event_usage WHERE operation_id=NEW.operation_id;
END;
CREATE INDEX IF NOT EXISTS pending_journal_events ON events(operation_id,sequence)
WHERE acknowledged=0;
