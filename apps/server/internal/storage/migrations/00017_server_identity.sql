-- +goose Up
CREATE TABLE server_identity (
 id INTEGER PRIMARY KEY CHECK(id=1),
 value TEXT NOT NULL CHECK(length(value)=32 AND value NOT GLOB '*[^a-f0-9]*')
);
INSERT INTO server_identity(id,value) VALUES(1,lower(hex(randomblob(16))));

-- +goose Down
-- Retain the identity binding stopped backups and maintenance receipts.
SELECT 1;
