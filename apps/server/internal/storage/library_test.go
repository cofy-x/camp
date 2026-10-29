package storage

import (
	"strings"
	"testing"
)

func TestPublicationRollbackAndActorIdempotency(t *testing.T) {
	s := testStore(t)
	if err := s.EnsureSetupToken(ctx, "setup"); err != nil {
		t.Fatal(err)
	}
	u, err := s.SetupAdmin(ctx, "setup", "alice", "Alice", strings.Repeat("p", 32), "session", "2099-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	actor := WithUser(ctx, u)
	r, err := s.CreateResource(actor, Resource{ID: "resource", Name: "Working files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	var beforeEvents int
	if err := s.db.QueryRow(`SELECT count(*) FROM workspace_events WHERE type='resource.updated' AND entity_id=?`, r.ID).Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TRIGGER reject_publication BEFORE INSERT ON resource_publications BEGIN SELECT RAISE(FAIL,'injected publication failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	in := PublishInput{ResourceID: r.ID, ExpectedCommit: r.CurrentCommit, Commit: strings.Repeat("b", 40), Kind: "edit", Path: "notes.md", RequestID: "save", RequestHash: strings.Repeat("a", 64)}
	if _, err := s.Publish(actor, in); err == nil {
		t.Fatal("injected publication failure was ignored")
	}
	current, _ := s.GetResource(actor, r.ID)
	history, _ := s.ListPublications(actor, r.ID, 0)
	if current.CurrentCommit != r.CurrentCommit || len(history) != 0 {
		t.Fatal("failed publication changed durable state")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_publication`); err != nil {
		t.Fatal(err)
	}
	first, err := s.Publish(actor, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.Publish(actor, in)
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry: %#v %v", again, err)
	}
	var events int
	if err := s.db.QueryRow(`SELECT count(*) FROM workspace_events WHERE type='resource.updated' AND entity_id=?`, r.ID).Scan(&events); err != nil || events != beforeEvents+1 {
		t.Fatalf("publication notifications: %d %v", events, err)
	}
}
