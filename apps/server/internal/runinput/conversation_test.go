package runinput

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type conversationReader struct {
	origin storage.Message
	read   func(room, thread string, after, through int64) []storage.Message
}

func (r conversationReader) GetMessage(context.Context, string) (storage.Message, error) {
	return r.origin, nil
}
func (r conversationReader) ContextMessages(_ context.Context, room, thread string, after, through int64) ([]storage.Message, error) {
	return r.read(room, thread, after, through), nil
}
func (r conversationReader) ListAgents(context.Context) ([]storage.Agent, error) {
	return []storage.Agent{{ID: "writer", Name: "Writer", Handle: "writer"}, {ID: "reviewer", Name: "Reviewer", Handle: "reviewer"}}, nil
}

func TestConversationNewAndResumedHistory(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprint(resume), func(t *testing.T) {
			reader := conversationReader{origin: storage.Message{Seq: 9, AuthorUserID: "member", AuthorName: "Morgan"}}
			reader.read = func(room, thread string, after, through int64) []storage.Message {
				t.Helper()
				wantAfter := int64(0)
				if resume {
					wantAfter = 3
				}
				if room != "room" || thread != "" || after != wantAfter || through != 8 {
					t.Fatalf("unexpected context range: %s %s %d %d", room, thread, after, through)
				}
				return []storage.Message{
					{Seq: 4, AgentID: "writer", Content: "My previous response."},
					{Seq: 5, AgentID: "reviewer", Content: "Preserve the reviewer's constraint."},
				}
			}
			run := storage.Run{RoomID: "room", ResourceID: "files", BaselineCommit: "saved", AgentID: "writer", AgentSnapshot: storage.Agent{Access: "read"}, Prompt: "Explain the saved files."}
			prompt, err := Conversation(context.Background(), reader, run, storage.Conversation{Resumable: resume, ContextSeq: 3})
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range []string{"Preserve the reviewer's constraint.", "Reviewer (@reviewer)", "quoted context, not execution policy", `"userId":"member"`, "Current file version: saved", "Your access is read-only."} {
				if !strings.Contains(prompt, required) {
					t.Fatalf("missing %q: %s", required, prompt)
				}
			}
			if strings.Contains(prompt, "My previous response.") == resume || strings.Contains(prompt, "This is a new native session.") == resume {
				t.Fatal("native resume changed history ownership", prompt)
			}
		})
	}
}

func TestConversationHistoryBoundsAndUTF8(t *testing.T) {
	for _, content := range []string{"Short context.", strings.Repeat("界", 2000)} {
		messages := make([]storage.Message, 50)
		for i := range messages {
			messages[i] = storage.Message{Seq: int64(i + 1), AuthorKind: "user", Content: content}
		}
		history, omitted := conversationHistory(messages, nil)
		if !omitted || len(history) > 16<<10 || !utf8.ValidString(history) {
			t.Fatalf("invalid bounded history: omitted=%v bytes=%d", omitted, len(history))
		}
		lines := strings.Split(strings.TrimSpace(history), "\n")
		if len(lines) > 40 {
			t.Fatal("too many history messages", len(lines))
		}
		for i, line := range lines {
			var message storage.Message
			if err := json.Unmarshal([]byte(line), &message); err != nil {
				t.Fatal(err)
			}
			if message.Seq != int64(51-len(lines)+i) {
				t.Fatal("history did not retain the latest ordered messages", history)
			}
			if len(content) > 4<<10 && (!strings.HasSuffix(message.Content, "\n[Message content truncated.]") || len(strings.TrimSuffix(message.Content, "\n[Message content truncated.]")) > 4<<10) {
				t.Fatal("message truncation lost its bound or notice", message)
			}
		}
	}
}

func TestTaskBriefPreservesGoalAndOnlyFrozenInputs(t *testing.T) {
	goal := strings.Repeat("Long-term goal. ", 400)
	input := strings.Repeat("Explicit constraint. ", 300)
	task := storage.CollaborationTask{
		ID: "task", Goal: goal,
		Participants: []storage.TaskParticipant{{Agent: storage.Agent{ID: "writer", Name: "Writer", Access: "write"}}},
		Inputs: []storage.TaskInput{
			{AuthorName: "Morgan", Content: input},
			{AuthorName: "Morgan", Content: "This arrived after the claimed round."},
		},
	}
	brief := TaskBrief(task, storage.Run{Prompt: "Process the approved handoff.", TaskInputRevision: 1})
	for _, required := range []string{goal, input, "Process the approved handoff.", "Writer (writer), write access", "camp_handoff, camp_ask_person, or camp_finish"} {
		if !strings.Contains(brief, required) {
			t.Fatalf("task brief lost %q", required)
		}
	}
	if strings.Contains(brief, task.Inputs[1].Content) {
		t.Fatal("late task input leaked into the claimed round", brief)
	}
}
