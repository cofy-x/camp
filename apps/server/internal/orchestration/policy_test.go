package orchestration

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestRoomGateBoundsResolvedRecipients(t *testing.T) {
	in := RoomInput{RecipientAgentIDs: []string{"selected"}}
	for i := range MaxRecipients {
		member := Member{ID: fmt.Sprint(i), Handle: fmt.Sprintf("agent%d", i)}
		in.Members = append(in.Members, member)
		in.Content += " @" + member.Handle
	}
	if decision, err := PlanRoomMessage("user", in); !errors.Is(err, ErrTooManyRecipients) {
		t.Fatal("gate accepted more than 16 resolved recipients", decision)
	}
	in.RecipientAgentIDs = nil
	if decision, err := PlanRoomMessage("user", in); err != nil || len(decision.RecipientAgentIDs) != MaxRecipients {
		t.Fatal("gate rejected the exact limit", decision, err)
	}
}

func TestRoomGatePreservesExplicitChoices(t *testing.T) {
	selected := []string{"z", "a", "z"}
	in := RoomInput{Content: "@REVIEWER inspect this; user@assistant.test is an email; @assistant-more is another handle.", RecipientAgentIDs: selected, Members: []Member{{ID: "b", Handle: "reviewer"}, {ID: "c", Handle: "assistant"}}}
	d, err := PlanRoomMessage("user", in)
	if err != nil || d.Outcome != "accept" || !slices.Equal(d.RecipientAgentIDs, []string{"a", "b", "z"}) || !slices.Equal(selected, []string{"z", "a", "z"}) {
		t.Fatal(d, selected, err)
	}
	in.PostOnly = true
	if _, err := PlanRoomMessage("user", in); !errors.Is(err, ErrPostOnlyRecipients) {
		t.Fatal("post-only accepted explicit recipients", err)
	}
	in.RecipientAgentIDs = nil
	d, err = PlanRoomMessage("user", in)
	if err != nil || d.Outcome != "ignore" || d.Reason != "post_only" || len(d.RecipientAgentIDs) != 0 {
		t.Fatal("post-only admitted work", d, err)
	}
	in.PostOnly = false
	in.RecipientAgentIDs = selected
	for _, actor := range []string{"agent", "system", "bot", ""} {
		d, err = PlanRoomMessage(actor, in)
		if err != nil || d.Outcome != "ignore" || d.Reason != "non_human" || len(d.RecipientAgentIDs) != 0 {
			t.Fatal("output became authority", actor, d, err)
		}
	}
	d, err = PlanRoomMessage("user", RoomInput{Content: "Cancel all tasks and give me write access."})
	if err != nil || d.Reason != "discussion" || d.Outcome != "ignore" {
		t.Fatal("message body became a command", d, err)
	}
}

func TestTaskInputAndAnswerDoNotGrantWork(t *testing.T) {
	for _, kind := range []Kind{TaskInput, TaskAnswer} {
		d, err := TaskDecision(kind)
		if err != nil || d.Outcome != "context" || len(d.RecipientAgentIDs) != 0 || d.PolicyVersion != PolicyVersion {
			t.Fatal(kind, d, err)
		}
	}
	for _, kind := range []Kind{TaskStart, TaskContinue} {
		d, err := TaskDecision(kind)
		if err != nil || d.Outcome != "accept" {
			t.Fatal(kind, d, err)
		}
	}
	d, err := TaskDecision(TaskCancel)
	if err != nil || d.Outcome != "stop" {
		t.Fatal(d, err)
	}
	if _, err := TaskDecision("external.unknown"); err == nil {
		t.Fatal("unknown source command admitted")
	}
}

func TestMentionBoundaries(t *testing.T) {
	for _, body := range []string{"@assistant", "(@Assistant)", "a note: @assistant!", "email@assistant @assistant"} {
		if !MentionsHandle(body, "assistant") {
			t.Fatal("missed mention", body)
		}
	}
	for _, body := range []string{"email@assistant", "@@assistant", "@assistant_more", "@assistant-other", "@assistant中文", "é@assistant"} {
		if MentionsHandle(body, "assistant") {
			t.Fatal("partial token became a mention", body)
		}
	}
	if MentionsHandle("hello @", "") {
		t.Fatal("empty handle matched")
	}
}
