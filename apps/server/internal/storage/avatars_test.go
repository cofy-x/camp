package storage

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/avatars"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func otherAvatar(kind, current string) string {
	for _, entry := range avatars.Catalog() {
		if entry.Kind == kind && entry.ID != current {
			return entry.ID
		}
	}
	panic("missing alternative avatar")
}

func TestMemberAvatarCompareAndSwapAndAuthority(t *testing.T) {
	s := testStore(t)
	admin, member := teamUsers(t, s)
	person, _ := UserFromContext(member)
	wanted := otherAvatar(avatars.Member, person.Avatar)
	changed, err := s.UpdateOwnAvatar(member, wanted, person.Avatar)
	if err != nil || changed.Avatar != wanted || changed.ID != person.ID {
		t.Fatal(changed, err)
	}
	replayed, err := s.UpdateOwnAvatar(member, wanted, person.Avatar)
	if err != nil || replayed != changed {
		t.Fatal("lost-response retry changed the saved profile", replayed, err)
	}
	_, err = s.UpdateOwnAvatar(member, person.Avatar, person.Avatar)
	requireCode(t, err, "profile_conflict")
	_, err = s.UpdateOwnAvatar(member, otherAvatar(avatars.Agent, ""), wanted)
	requireCode(t, err, "invalid_avatar")
	_, err = s.UpdateOwnAvatar(ctx, wanted, wanted)
	requireCode(t, err, "unauthenticated")
	disabled := false
	if _, err := s.UpdateUserAccess(admin, person.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateOwnAvatar(member, wanted, wanted)
	requireCode(t, err, "unauthenticated")
}

func TestAgentAvatarUsesExistingOwnershipRevisionAndFrozenInputs(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, member := teamUsers(t, s)
	a, err := s.SaveAgent(admin, Agent{Name: "Writer", Handle: "owned-writer", Access: "write", Enabled: true})
	if err != nil || !avatars.Valid(a.Avatar, avatars.Agent) {
		t.Fatal(a, err)
	}
	if err := s.JoinRoom(admin, room.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	sent, err := s.SendMessage(admin, room.ID, SendMessageInput{RequestID: "avatar-snapshot", Content: "Keep the original identity.", RecipientAgentIDs: []string{a.ID}}, roomBudget)
	if err != nil || len(sent.Runs) != 1 {
		t.Fatal(sent, err)
	}
	before := sent.Runs[0]
	wanted := otherAvatar(avatars.Agent, a.Avatar)
	change := a
	change.Avatar = wanted
	_, err = s.SaveAgent(member, change)
	requireCode(t, err, "forbidden")
	saved, err := s.SaveAgent(admin, change)
	if err != nil || saved.Avatar != wanted || saved.Revision != a.Revision+1 {
		t.Fatal(saved, err)
	}
	_, err = s.SaveAgent(admin, change)
	requireCode(t, err, "revision_conflict")
	saved.Avatar = otherAvatar(avatars.Member, "")
	_, err = s.SaveAgent(admin, saved)
	requireCode(t, err, "invalid_avatar")
	frozen, err := s.GetRun(ctx, before.ID)
	if err != nil || frozen.AgentSnapshot.Avatar != before.AgentSnapshot.Avatar || frozen.AgentSnapshot.Revision != before.AgentSnapshot.Revision {
		t.Fatal("profile update modified an admitted execution", frozen, err)
	}
}

func TestAvatarDefaultsPersistAndAvoidRepeats(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "avatars.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seen := map[string]bool{}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for i := range 16 {
			user, err := createUser(ctx, q, fmt.Sprintf("member-%02d", i), "Member", strings.Repeat("h", 64), "member")
			if err != nil {
				return err
			}
			if seen[user.Avatar] || !avatars.Valid(user.Avatar, avatars.Member) {
				t.Fatal("repeated or missing default", user)
			}
			seen[user.Avatar] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListUsers(ctx)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ListUsers(ctx)
	if err != nil || fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("restart reassigned avatars", err)
	}
}

func TestAvatarUpgradePreservesDataAndRetriesMissingBackfill(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 10)); err != nil {
		t.Fatal(err)
	}
	const snapshot = `{"id":"historical","agentSnapshot":{"id":"agent","icon":"notes","revision":7},"prompt":"Keep this exactly"}`
	_, err := s.db.Exec(`
INSERT INTO users(id,username,name,role,enabled,password_hash,created_at,updated_at) VALUES('member','member','Member','admin',1,'hash','then','then');
INSERT INTO agents(id,name,handle,icon,instructions,access,enabled,revision,created_at,updated_at) VALUES('agent','Agent','agent','notes','Keep instructions','read',1,7,'then','then');
INSERT INTO resources(id,name,source_path,current_commit,environment,created_at,updated_at) VALUES('files','Files','','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}','then','then');
INSERT INTO rooms(id,name,description,resource_id,created_at,updated_at) VALUES('room','Room','','files','then','then');
INSERT INTO messages(id,room_id,seq,author_kind,author_user_id,author_name,content,created_at,updated_at) VALUES('message','room',1,'user','member','Member','Keep the discussion','then','then');
INSERT INTO runs(id,room_id,origin_message_id,agent_id,resource_id,status,snapshot,created_at,updated_at) VALUES('historical','room','message','agent','files','succeeded',?,'then','then');`, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	member, err := s.GetUser(ctx, "member")
	if err != nil || !avatars.Valid(member.Avatar, avatars.Member) || member.UpdatedAt != "then" {
		t.Fatal(member, err)
	}
	agent, err := s.GetAgent(ctx, "agent")
	if err != nil || !avatars.Valid(agent.Avatar, avatars.Agent) || agent.Revision != 7 || agent.UpdatedAt != "then" || agent.Instructions != "Keep instructions" {
		t.Fatal(agent, err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT snapshot FROM runs WHERE id='historical'`).Scan(&raw); err != nil || raw != snapshot {
		t.Fatal("migration rewrote historical execution", raw, err)
	}
	messages, err := s.ListMessages(ctx, "room", "", 0)
	if err != nil || len(messages) != 1 || messages[0].AuthorUserID != member.ID || messages[0].Content != "Keep the discussion" {
		t.Fatal(messages, err)
	}
	// A backfill failure rolls back both kinds and is safely retried at startup.
	if _, err := s.db.Exec(`UPDATE users SET avatar=''; UPDATE agents SET avatar=''; CREATE TRIGGER reject_avatar BEFORE UPDATE OF avatar ON agents BEGIN SELECT RAISE(ABORT,'injected backfill failure'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.ensureAvatars(ctx); err == nil {
		t.Fatal("accepted a failed backfill")
	}
	assertScalar(t, s, `SELECT count(*) FROM users WHERE avatar=''`, 1)
	if _, err := s.db.Exec(`DROP TRIGGER reject_avatar`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, `SELECT count(*) FROM users WHERE avatar=''`, 0)
	assertScalar(t, s, `SELECT count(*) FROM agents WHERE avatar=''`, 0)
	member, _ = s.GetUser(ctx, "member")
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	again, _ := s.GetUser(ctx, "member")
	if again != member {
		t.Fatal("repeat startup modified an assigned avatar")
	}
}
