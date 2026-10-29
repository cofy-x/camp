package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

var roomBudget = Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180}

func roomFixture(t *testing.T) (*Store, Room, Agent) {
	t.Helper()
	s := testStore(t)
	r, e := s.SeedRoom(ctx, Resource{ID: "resource", Name: "Shared files", CurrentCommit: strings.Repeat("a", 40)})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.GetAgent(ctx, r.DefaultAgentID)
	if e != nil {
		t.Fatal(e)
	}
	return s, r, a
}
func sendRoom(t *testing.T, s *Store, r Room, key string) SendResult {
	t.Helper()
	v, e := sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: key, Content: "Prepare a short document."}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestRoomSeedAndExplicitRouting(t *testing.T) {
	s, r, a := roomFixture(t)
	again, e := s.SeedRoom(ctx, Resource{ID: "ignored"})
	if e != nil || again.ID != r.ID {
		t.Fatalf("reseed: %#v %v", again, e)
	}
	reviewer, e := s.SaveAgent(ctx, Agent{Name: "Reviewer", Handle: "reviewer", Access: "read", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.JoinRoom(ctx, r.ID, reviewer.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.JoinRoom(ctx, r.ID, reviewer.ID); e != nil {
		t.Fatal(e)
	}
	members, _ := s.ListMembers(ctx, r.ID)
	if len(members) != 2 {
		t.Fatal(members)
	}
	v, e := sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: "both", Content: "Please inspect this.", RecipientAgentIDs: []string{a.ID, reviewer.ID, a.ID}}, roomBudget)
	if e != nil || len(v.Runs) != 2 {
		t.Fatalf("send: %#v %v", v, e)
	}
	note, e := sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: "note", Content: "A note.", PostOnly: true}, roomBudget)
	if e != nil || len(note.Runs) != 0 {
		t.Fatalf("note: %#v %v", note, e)
	}
	outsider, _ := s.SaveAgent(ctx, Agent{Name: "Other", Handle: "other", Access: "read", Enabled: true})
	_, e = sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: "bad", Content: "Hello", RecipientAgentIDs: []string{outsider.ID}}, roomBudget)
	requireCode(t, e, "invalid_agent")
	messages, _ := s.ListMessages(ctx, r.ID, "", 0)
	if len(messages) != 2 {
		t.Fatal("invalid request persisted", messages)
	}
}
func TestRoomSendConcurrentIdempotencyAndFrozenIdentity(t *testing.T) {
	s, r, a := roomFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			v, e := sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: "repeat", Content: "One request"}, roomBudget)
			if e != nil {
				t.Error(e)
				return
			}
			ids <- v.Message.ID
		})
	}
	wg.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("duplicate messages")
		}
	}
	runs, _ := s.ListQueuedRuns(ctx)
	if len(runs) != 1 {
		t.Fatal(runs)
	}
	_, e := sendToAssistant(s, ctx, r.ID, SendMessageInput{RequestID: "repeat", Content: "Different request"}, roomBudget)
	requireCode(t, e, "request_conflict")
	a.Instructions = "Changed after send"
	if _, e = s.SaveAgent(ctx, a); e != nil {
		t.Fatal(e)
	}
	other, e := s.CreateResource(ctx, Resource{ID: "other-resource", Name: "Other", CurrentCommit: strings.Repeat("b", 40), Environment: json.RawMessage(`{"checks":[]}`)})
	if e != nil {
		t.Fatal(e)
	}
	r.ResourceID = other.ID
	if _, e = s.UpdateRoom(ctx, r); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.ClaimRun(ctx, runs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	if claimed.ResourceID == other.ID || claimed.AgentSnapshot.Instructions == a.Instructions || claimed.BaselineCommit != strings.Repeat("a", 40) {
		t.Fatalf("mutable admission: %#v", claimed)
	}
}
func TestClaimSerializesAgentAndChecksMembership(t *testing.T) {
	s, r, a := roomFixture(t)
	one := sendRoom(t, s, r, "one")
	two := sendRoom(t, s, r, "two")
	active, e := s.ClaimRun(ctx, one.Runs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ClaimRun(ctx, two.Runs[0].ID)
	requireCode(t, e, "run_busy")
	active.Status = "cancelled"
	if _, e = s.FinishRun(ctx, active, "Stopped.", false); e != nil {
		t.Fatal(e)
	}
	if e = s.LeaveRoom(ctx, r.ID, a.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.ClaimRun(ctx, two.Runs[0].ID)
	requireCode(t, e, "agent_unavailable")
	fresh, _ := s.GetRoom(ctx, r.ID)
	if fresh.DefaultAgentID != "" {
		t.Fatal("default was retained")
	}
}
func TestFinishPublishesAndPostsExactlyOnce(t *testing.T) {
	s, r, _ := roomFixture(t)
	turn := sendRoom(t, s, r, "work")
	run, e := s.ClaimRun(ctx, turn.Runs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.GetConversation(ctx, run)
	if e != nil {
		t.Fatal(e)
	}
	run.SessionID = session.SessionID
	run.SessionGeneration = session.Generation
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	run.Files = []RunFile{{Path: "report.md", Status: "added", After: "# Report"}}
	saved, e := s.FinishRun(ctx, run, "The report is ready.", true)
	if e != nil || !saved.Published {
		t.Fatalf("finish %#v %v", saved, e)
	}
	if _, e = s.FinishRun(ctx, run, "Duplicate", true); e != nil {
		t.Fatal(e)
	}
	messages, _ := s.ListMessages(ctx, r.ID, "", 0)
	if len(messages) != 2 || messages[1].Content != "The report is ready." {
		t.Fatal(messages)
	}
	resource, _ := s.GetResource(ctx, run.ResourceID)
	if resource.CurrentCommit != run.ResultCommit {
		t.Fatal(resource)
	}
	newRun := sendRoom(t, s, r, "followup").Runs[0]
	newRun, _ = s.ClaimRun(ctx, newRun.ID)
	next, e := s.GetConversation(ctx, newRun)
	if e != nil || !next.Resumable || next.SessionID != session.SessionID || next.ContextSeq != turn.Message.Seq {
		t.Fatalf("resume %#v %v", next, e)
	}
	if next, e = s.GetConversation(ctx, newRun); e != nil || next.Resumable || next.Generation != session.Generation+1 {
		t.Fatalf("in-use generation was resumed: %#v %v", next, e)
	}
}
func TestFinalizationFailureRollsBackResourceMessageAndSession(t *testing.T) {
	s, r, _ := roomFixture(t)
	turn := sendRoom(t, s, r, "work")
	run, _ := s.ClaimRun(ctx, turn.Runs[0].ID)
	session, _ := s.GetConversation(ctx, run)
	run.SessionID = session.SessionID
	run.SessionGeneration = session.Generation
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	if _, e := s.db.Exec(`CREATE TRIGGER fail_final BEFORE INSERT ON messages WHEN NEW.author_kind='agent' BEGIN SELECT RAISE(ABORT,'simulated write failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.FinishRun(ctx, run, "Done", true); e == nil {
		t.Fatal("expected failure")
	}
	stored, _ := s.GetRun(ctx, run.ID)
	resource, _ := s.GetResource(ctx, run.ResourceID)
	messages, _ := s.ListMessages(ctx, r.ID, "", 0)
	if stored.Status != "preparing" || resource.CurrentCommit != run.BaselineCommit || len(messages) != 1 {
		t.Fatalf("partial write %s %s %d", stored.Status, resource.CurrentCommit, len(messages))
	}
	if _, e := s.db.Exec(`DROP TRIGGER fail_final`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.FinishRun(ctx, run, "Done", true); e != nil {
		t.Fatal(e)
	}
}
func TestPublicationConflictPreservesResult(t *testing.T) {
	s, r, _ := roomFixture(t)
	turn := sendRoom(t, s, r, "work")
	run, _ := s.ClaimRun(ctx, turn.Runs[0].ID)
	if _, e := s.db.Exec(`UPDATE resources SET current_commit=? WHERE id=?`, strings.Repeat("c", 40), run.ResourceID); e != nil {
		t.Fatal(e)
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	finished, e := s.FinishRun(ctx, run, "Success", true)
	if e != nil || finished.Status != "failed" || finished.Published || finished.ResultCommit != run.ResultCommit {
		t.Fatalf("conflict %#v %v", finished, e)
	}
	resource, _ := s.GetResource(ctx, run.ResourceID)
	if resource.CurrentCommit != strings.Repeat("c", 40) {
		t.Fatal(resource)
	}
	msgs, _ := s.ListMessages(ctx, r.ID, "", 0)
	if !strings.Contains(msgs[1].Content, "retained separately") {
		t.Fatal(msgs)
	}
}
func TestThreadContextAndCursorDoNotLeakOtherThreads(t *testing.T) {
	s, r, _ := roomFixture(t)
	root := sendRoom(t, s, r, "root")
	other := sendRoom(t, s, r, "other")
	in := SendMessageInput{RequestID: "reply", Content: "Only this thread", ThreadRootID: root.Message.ID, PostOnly: true}
	reply, e := sendToAssistant(s, ctx, r.ID, in, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	messages, e := s.ListMessages(ctx, r.ID, root.Message.ID, 0)
	if e != nil || len(messages) != 2 || messages[0].ID != root.Message.ID || messages[1].ID != reply.Message.ID {
		t.Fatalf("thread %#v %v", messages, e)
	}
	after, _ := s.ListMessages(ctx, r.ID, root.Message.ID, root.Message.Seq)
	if len(after) != 1 {
		t.Fatal(after)
	}
	context, _ := s.ContextMessages(ctx, r.ID, root.Message.ID, 0, other.Message.Seq)
	if len(context) != 1 || context[0].ID != root.Message.ID {
		t.Fatal(context)
	}
	in.RequestID = "nested"
	in.ThreadRootID = reply.Message.ID
	_, e = sendToAssistant(s, ctx, r.ID, in, roomBudget)
	requireCode(t, e, "invalid_message")
}
func TestWorkspaceEventsCursorAndRecoverySelection(t *testing.T) {
	s, r, _ := roomFixture(t)
	queued := sendRoom(t, s, r, "queued")
	active := sendRoom(t, s, r, "active")
	if _, e := s.ClaimRun(ctx, active.Runs[0].ID); e != nil {
		t.Fatal(e)
	}
	unfinished, _ := s.ListUnfinishedRuns(ctx)
	if len(unfinished) != 1 || unfinished[0].ID != active.Runs[0].ID {
		t.Fatal(unfinished)
	}
	waiting, _ := s.ListQueuedRuns(ctx)
	if len(waiting) != 1 || waiting[0].ID != queued.Runs[0].ID {
		t.Fatal(waiting)
	}
	events, e := s.ListWorkspaceEvents(ctx, 0)
	if e != nil || len(events) == 0 {
		t.Fatal(e)
	}
	last := events[len(events)-1]
	room, _ := s.GetRoom(ctx, r.ID)
	if room.ActivitySeq != last.ID {
		t.Fatalf("room cursor %d event %d", room.ActivitySeq, last.ID)
	}
	tail, _ := s.ListWorkspaceEvents(ctx, last.ID)
	if len(tail) != 0 {
		t.Fatal(tail)
	}
}
func TestInterruptedSessionStartsNewGenerationAndConfigurationChangesReset(t *testing.T) {
	s, r, a := roomFixture(t)
	turn := sendRoom(t, s, r, "first")
	run, _ := s.ClaimRun(ctx, turn.Runs[0].ID)
	session, _ := s.GetConversation(ctx, run)
	run.SessionID = session.SessionID
	run.SessionGeneration = session.Generation
	run.Status = "interrupted"
	if _, e := s.FinishRun(ctx, run, "Service restarted", false); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 2; n++ {
		next := sendRoom(t, s, r, fmt.Sprint("next", n)).Runs[0]
		next, _ = s.ClaimRun(ctx, next.ID)
		c, e := s.GetConversation(ctx, next)
		if e != nil || c.Resumable || c.Generation != session.Generation+1 {
			t.Fatalf("generation %#v %v", c, e)
		}
		next.SessionID = c.SessionID
		next.SessionGeneration = c.Generation
		next.Status = "succeeded"
		if _, e = s.FinishRun(ctx, next, "Done", true); e != nil {
			t.Fatal(e)
		}
		session = c
		a.Instructions = "New role"
		a, e = s.SaveAgent(ctx, a)
		if e != nil {
			t.Fatal(e)
		}
	}
}

func TestRoomCreationAndSeedAreAtomic(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_room BEFORE INSERT ON rooms BEGIN SELECT RAISE(ABORT,'room write failed'); END`); err != nil {
		t.Fatal(err)
	}
	resource := Resource{ID: "initial", Name: "Files", CurrentCommit: strings.Repeat("a", 40)}
	if _, err := s.SeedRoom(ctx, resource); err == nil {
		t.Fatal("expected seed failure")
	}
	assertScalar(t, s, "SELECT count(*) FROM resources", 0)
	assertScalar(t, s, "SELECT count(*) FROM agents", 0)
	assertScalar(t, s, "SELECT count(*) FROM rooms", 0)
	if _, err := s.db.Exec(`DROP TRIGGER reject_room`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SeedRoom(ctx, resource); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateRoomWithResource(ctx, Room{Name: "invalid\nname"}, Resource{ID: "invalid-room-files", Name: "Files", CurrentCommit: strings.Repeat("b", 40)})
	requireCode(t, err, "invalid_room")
	assertScalar(t, s, "SELECT count(*) FROM resources", 1)
}

func TestContextNoticeIsIdempotentAndDoesNotReplaceFinalReply(t *testing.T) {
	s, room, _ := roomFixture(t)
	run := sendRoom(t, s, room, "request").Runs[0]
	for range 2 {
		if err := s.AddRunNotice(ctx, run.ID, "Context was rebuilt after an interrupted session."); err != nil {
			t.Fatal(err)
		}
	}
	run.Status = "cancelled"
	if _, err := s.FinishRun(ctx, run, "Stopped.", false); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(ctx, room.ID, "", 0)
	if err != nil || len(messages) != 3 || messages[1].AuthorKind != "system" || messages[2].AuthorKind != "agent" {
		t.Fatalf("notices %#v %v", messages, err)
	}
	queued, _ := s.ListQueuedRuns(ctx)
	if len(queued) != 0 {
		t.Fatal("system notice started work")
	}
}

func TestModelAndEnvironmentChangesResetNativeSession(t *testing.T) {
	s, room, _ := roomFixture(t)
	budget := roomBudget
	budget.Model = "model-one"
	budget.RuntimeKey = "runtime-one"
	send := func(request string) Run {
		t.Helper()
		turn, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: request, Content: "Continue"}, budget)
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.ClaimRun(ctx, turn.Runs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	finish := func(run Run, session Conversation) {
		t.Helper()
		run.SessionID = session.SessionID
		run.SessionGeneration = session.Generation
		run.Status = "succeeded"
		if _, err := s.FinishRun(ctx, run, "Done.", true); err != nil {
			t.Fatal(err)
		}
	}
	first := send("one")
	session, err := s.GetConversation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	finish(first, session)
	budget.Model = "model-two"
	budget.RuntimeKey = "runtime-two"
	second := send("two")
	next, err := s.GetConversation(ctx, second)
	if err != nil || next.Resumable || next.Generation != session.Generation+1 || second.Model != "model-two" {
		t.Fatalf("model reset: %#v %v", next, err)
	}
	finish(second, next)
	resource, _ := s.GetResource(ctx, room.ResourceID)
	resource.Environment = json.RawMessage(`{"checks":[]}`)
	if _, err = s.UpdateResource(ctx, resource); err != nil {
		t.Fatal(err)
	}
	third := send("three")
	changed, err := s.GetConversation(ctx, third)
	if err != nil || changed.Resumable || changed.Generation != next.Generation+1 {
		t.Fatalf("environment reset: %#v %v", changed, err)
	}
	third.Model = "mutated"
	third.RuntimeKey = "mutated"
	third.Status = "running"
	saved, err := s.UpdateRun(ctx, third)
	if err != nil || saved.Model != "model-two" || saved.RuntimeKey != "runtime-two" {
		t.Fatalf("frozen config %#v %v", saved, err)
	}
}

func TestFailedRunContextIncludesActualOutcomeAndAgentOutput(t *testing.T) {
	s, room, _ := roomFixture(t)
	run := sendRoom(t, s, room, "request").Runs[0]
	run, err := s.ClaimRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "failed"
	run.Error = "The configured test command exited with status 1."
	if _, err = s.FinishRun(ctx, run, "The implementation is ready.", false); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(ctx, room.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	final := messages[len(messages)-1].Content
	if !strings.HasPrefix(final, "Execution failed.") || !strings.Contains(final, run.Error) || !strings.Contains(final, "The implementation is ready.") {
		t.Fatal(final)
	}
	next := sendRoom(t, s, room, "followup")
	history, err := s.ContextMessages(ctx, room.ID, "", 0, next.Message.Seq)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[1].Content != final {
		t.Fatal(history)
	}
}

// Fixtures that exercise execution explicitly address the room's assistant.
func sendToAssistant(s *Store, ctx context.Context, roomID string, input SendMessageInput, budget Budget) (SendResult, error) {
	if !input.PostOnly && input.RecipientAgentIDs == nil {
		room, err := s.GetRoom(ctx, roomID)
		if err != nil {
			return SendResult{}, err
		}
		input.RecipientAgentIDs = []string{}
		if room.DefaultAgentID != "" {
			assistant, err := s.queries().GetAgentByHandle(ctx, "assistant")
			if err != nil {
				return SendResult{}, err
			}
			input.RecipientAgentIDs = []string{assistant.ID}
		}
	}
	return s.SendMessage(ctx, roomID, input, budget)
}
