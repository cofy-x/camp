package storage

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestRunReplyReadsExactOriginalWithoutFullRunOrThreadScan(t *testing.T) {
	var reads []string
	s := observedReadStore(t, func(query string) { reads = append(reads, query) })
	room, err := s.SeedRoom(ctx, Resource{ID: "reply-files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	sent := sendRoom(t, s, room, "reply")
	run := sent.Runs[0]
	if reply, err := s.GetRunReply(ctx, run.ID); err != nil || reply != nil {
		t.Fatal("unanswered execution has a reply", reply, err)
	}
	other, err := s.CreateRoom(ctx, Room{Name: "Other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	foreign := Message{ID: newID(), RoomID: other.ID, Seq: 1, AuthorKind: "agent", AgentID: run.AgentID, RunID: run.ID, Content: "Wrong Room", CreatedAt: now(), UpdatedAt: now()}
	if err = s.transaction(ctx, func(tx *sql.Tx) error { return insertMessage(ctx, sqlgen.New(tx), foreign, "") }); err != nil {
		t.Fatal(err)
	}
	if reply, err := s.GetRunReply(ctx, run.ID); err != nil || reply != nil {
		t.Fatal("a foreign Room message was accepted", reply, err)
	}
	// Another Run has its own original reply; the first one's inconsistent
	// historical message must neither leak across Rooms nor match this Run.
	sent = sendRoom(t, s, room, "real-reply")
	run = sent.Runs[0]
	run.Status = "succeeded"
	if _, err = s.FinishRun(ctx, run, "Original response.", false); err != nil {
		t.Fatal(err)
	}
	reads = nil
	reply, err := s.GetRunReply(ctx, run.ID)
	if err != nil || reply == nil || reply.RunID != run.ID || reply.RoomID != room.ID || reply.AuthorKind != "agent" || reply.Content != "Original response." {
		t.Fatal("exact reply lost", reply, err)
	}
	for _, query := range reads {
		if strings.Contains(query, "-- name: GetRun :one") || strings.Contains(query, "-- name: ListMessages :many") {
			t.Fatal("reply loaded a full Run or thread")
		}
	}
	original, err := s.GetMessage(ctx, reply.ID)
	if err != nil || !reflect.DeepEqual(reply, &original) {
		t.Fatal("reply projection changed original message facts", reply, original, err)
	}
	_, err = s.GetRunReply(ctx, "missing")
	requireCode(t, err, "not_found")
}
