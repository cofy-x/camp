-- +goose Up
ALTER TABLE agents ADD COLUMN owner_user_id TEXT REFERENCES users(id);
ALTER TABLE agents ADD COLUMN preferred_runner_id TEXT REFERENCES runners(id);
ALTER TABLE runners ADD COLUMN owner_user_id TEXT REFERENCES users(id);
ALTER TABLE runners ADD COLUMN team_shared INTEGER NOT NULL DEFAULT 1 CHECK(team_shared IN (0,1));
ALTER TABLE runner_enrollments ADD COLUMN owner_user_id TEXT REFERENCES users(id);

CREATE TABLE runner_agent_grants (
    runner_id TEXT NOT NULL REFERENCES runners(id),
    agent_id TEXT NOT NULL REFERENCES agents(id),
    created_at TEXT NOT NULL,
    PRIMARY KEY(runner_id,agent_id)
);
CREATE TABLE personal_runner_defaults (
    user_id TEXT PRIMARY KEY REFERENCES users(id),
    runner_id TEXT REFERENCES runners(id),
    revision INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE team_runner_defaults (
    id TEXT PRIMARY KEY CHECK(id='default'),
    runner_id TEXT REFERENCES runners(id),
    revision INTEGER NOT NULL DEFAULT 0
);
INSERT INTO team_runner_defaults(id) VALUES('default');
CREATE INDEX agents_owner ON agents(owner_user_id);
CREATE INDEX runners_owner ON runners(owner_user_id);

-- +goose Down
DROP TABLE team_runner_defaults;
DROP TABLE personal_runner_defaults;
DROP TABLE runner_agent_grants;
DROP INDEX agents_owner;
DROP INDEX runners_owner;
ALTER TABLE runner_enrollments DROP COLUMN owner_user_id;
ALTER TABLE runners DROP COLUMN team_shared;
ALTER TABLE runners DROP COLUMN owner_user_id;
ALTER TABLE agents DROP COLUMN preferred_runner_id;
ALTER TABLE agents DROP COLUMN owner_user_id;
