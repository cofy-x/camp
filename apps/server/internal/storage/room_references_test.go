package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func completedReferenceSource(t *testing.T, s *Store, room Room, key, text string) (Message, Run) {
	t.Helper()
	turn := sendRoom(t, s, room, key)
	run, err := s.ClaimRun(ctx, turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	run.Checks = json.RawMessage(`[{"name":"test","status":"passed","output":"tool output is not an excerpt"}]`)
	run, err = s.FinishRun(ctx, run, text, false)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(ctx, room.ID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return messages[len(messages)-1], run
}

func TestReferencesFreezeEvidenceWithMessageAndRun(t *testing.T) {
	s, room, agent := roomFixture(t)
	source, sourceRun := completedReferenceSource(t, s, room, "source", "A report is ready.\nReview the accessibility section.")
	input := SendMessageInput{RecipientAgentIDs: []string{agent.ID}, RequestID: "review", Content: "Review this evidence.", ThreadRootID: source.ID, References: []ReferenceInput{
		{Kind: "message", MessageID: source.ID},
		{Kind: "file", ResourceID: room.ResourceID, Commit: sourceRun.ResultCommit, Path: "docs/report.md"},
	}}
	accepted, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	refs := accepted.Message.References
	if len(refs) != 2 || len(accepted.Runs) != 1 || !reflect.DeepEqual(refs, accepted.Runs[0].References) {
		t.Fatalf("inconsistent evidence: %#v", accepted)
	}
	if refs[0].Content != source.Content || refs[0].Author != agent.Name || refs[0].SourceStatus != "succeeded" || refs[0].Published == nil || !*refs[0].Published || refs[0].Checks != "test: passed" || refs[0].SourceRunID != sourceRun.ID || refs[0].ResultCommit != sourceRun.ResultCommit {
		t.Fatalf("missing authoritative result metadata: %#v", refs[0])
	}
	if refs[1].Content != "" || refs[1].Commit != sourceRun.ResultCommit || refs[1].SourceRunID != sourceRun.ID || refs[1].Title != "Shared files / docs/report.md" {
		t.Fatalf("file reference: %#v", refs[1])
	}
	agent.Name = "Renamed after admission"
	if _, err := s.SaveAgent(ctx, agent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE resources SET current_commit=? WHERE id=?`, strings.Repeat("c", 40), room.ResourceID); err != nil {
		t.Fatal(err)
	}
	if err := s.LeaveRoom(ctx, room.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Message, accepted.Message) || len(replayed.Runs) != 1 {
		t.Fatalf("retry changed accepted snapshots: %#v %v", replayed, err)
	}
	stored, err := s.GetRoomMessage(ctx, room.ID, accepted.Message.ID)
	if err != nil || !reflect.DeepEqual(stored.References, refs) {
		t.Fatalf("persisted evidence changed: %#v %v", stored, err)
	}
	changed := input
	changed.References = []ReferenceInput{input.References[1], input.References[0]}
	_, err = sendToAssistant(s, ctx, room.ID, changed, roomBudget)
	requireCode(t, err, "request_conflict")
	changed.References = nil
	_, err = sendToAssistant(s, ctx, room.ID, changed, roomBudget)
	requireCode(t, err, "request_conflict")
}

func TestReferenceValidationRejectsUnrelatedAndUnknownEvidenceAtomically(t *testing.T) {
	s, room, _ := roomFixture(t)
	source := sendRoom(t, s, room, "source").Message
	other, err := s.CreateRoomWithResource(ctx, Room{Name: "other"}, Resource{ID: "other-files", Name: "Other files", CurrentCommit: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	otherSource, err := sendToAssistant(s, ctx, other.ID, SendMessageInput{RequestID: "other", Content: "Private to another room", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	file := ReferenceInput{Kind: "file", ResourceID: room.ResourceID, Commit: strings.Repeat("a", 40), Path: "report.md"}
	cases := []struct {
		name string
		refs []ReferenceInput
	}{
		{"unknown message", []ReferenceInput{{Kind: "message", MessageID: "missing"}}},
		{"other room message", []ReferenceInput{{Kind: "message", MessageID: otherSource.Message.ID}}},
		{"mixed fields", []ReferenceInput{{Kind: "message", MessageID: source.ID, Path: "report.md"}}},
		{"unknown kind", []ReferenceInput{{Kind: "run", MessageID: source.ID}}},
		{"duplicate", []ReferenceInput{file, file}},
		{"too many", make([]ReferenceInput, MaxReferences+1)},
	}
	for _, path := range []string{"../secret", "/absolute", "folder/../report.md", "folder//report.md", ".", "report\n.md", "folder\\report.md"} {
		ref := file
		ref.Path = path
		cases = append(cases, struct {
			name string
			refs []ReferenceInput
		}{"path " + path, []ReferenceInput{ref}})
	}
	for _, commit := range []string{"HEAD", strings.Repeat("a", 40) + "^", strings.Repeat("d", 40), strings.Repeat("a", 64), strings.Repeat("A", 40)} {
		ref := file
		ref.Commit = commit
		cases = append(cases, struct {
			name string
			refs []ReferenceInput
		}{"revision " + commit, []ReferenceInput{ref}})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "invalid", Content: "Inspect", References: test.refs}, roomBudget)
			requireCode(t, err, "invalid_reference")
		})
	}
	assertScalar(t, s, "SELECT count(*) FROM messages", 2)
	assertScalar(t, s, "SELECT count(*) FROM runs", 1)
	_, err = s.GetRoomMessage(ctx, room.ID, otherSource.Message.ID)
	requireCode(t, err, "not_found")
}

func TestRetainedRoomResourcesAndExactRevisionsRemainReferenceable(t *testing.T) {
	s, room, _ := roomFixture(t)
	_, completed := completedReferenceSource(t, s, room, "source", "Done.")
	oldResource := room.ResourceID
	resource, err := s.CreateResource(ctx, Resource{Name: "Replacement files", CurrentCommit: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	room.ResourceID = resource.ID
	if _, err = s.UpdateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	for _, commit := range []string{completed.BaselineCommit, completed.ResultCommit} {
		input := SendMessageInput{RequestID: commit, Content: "Use the retained evidence.", PostOnly: true, References: []ReferenceInput{{Kind: "file", ResourceID: oldResource, Commit: commit, Path: "report.md"}}}
		accepted, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
		if err != nil || len(accepted.Runs) != 0 || accepted.Message.References[0].Commit != commit {
			t.Fatalf("retained %s: %#v %v", commit, accepted, err)
		}
	}
	input := SendMessageInput{RequestID: "frozen", Content: "Read original evidence and current files.", References: []ReferenceInput{{Kind: "file", ResourceID: oldResource, Commit: completed.BaselineCommit, Path: "report.md"}}}
	accepted, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.ClaimRun(ctx, accepted.Runs[0].ID)
	if err != nil || run.BaselineCommit != resource.CurrentCommit || run.References[0].Commit != completed.BaselineCommit {
		t.Fatalf("baseline replaced evidence: %#v %v", run, err)
	}
	run.References = []Reference{{Kind: "message", Content: "Mutation"}}
	run.Status = "running"
	run, err = s.UpdateRun(ctx, run)
	if err != nil || !reflect.DeepEqual(run.References, accepted.Message.References) {
		t.Fatalf("immutable run references changed: %#v %v", run, err)
	}
}

func TestMessageReferenceExcerptsAreUTF8BoundedAndTotalIsAtomic(t *testing.T) {
	s, room, _ := roomFixture(t)
	text := strings.Repeat("界", MaxReferenceTextSize)
	refs := []ReferenceInput{}
	for _, key := range []string{"one", "two", "three", "four", "five"} {
		source, _ := completedReferenceSource(t, s, room, key, text)
		refs = append(refs, ReferenceInput{Kind: "message", MessageID: source.ID})
	}
	accepted, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "four-excerpts", Content: "Review four excerpts.", PostOnly: true, References: refs[:4]}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range accepted.Message.References {
		if !ref.Truncated || !utf8.ValidString(ref.Content) || len(ref.Content) > MaxReferenceTextSize || len(ref.Content) < MaxReferenceTextSize-3 {
			t.Fatalf("invalid excerpt: %d bytes, truncated=%v", len(ref.Content), ref.Truncated)
		}
	}
	_, err = sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "overflow", Content: "Review five excerpts.", References: refs}, roomBudget)
	requireCode(t, err, "invalid_reference")
	assertScalar(t, s, "SELECT count(*) FROM messages WHERE request_id='overflow'", 0)
}
