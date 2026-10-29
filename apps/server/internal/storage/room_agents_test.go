package storage

import (
	"reflect"
	"testing"
)

func TestCreateRoomAgentCommitsIdentityAndMembershipWithoutWork(t *testing.T) {
	s, room, assistant := roomFixture(t)
	before, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.CreateRoomAgent(ctx, room.ID, Agent{Name: " Reviewer ", Handle: " REVIEWER ", Instructions: "Review documents for clarity.", Access: "read", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Name != "Reviewer" || agent.Handle != "reviewer" || agent.Icon != "agent" || agent.Revision != 1 || agent.ID == "" || agent.CreatedAt == "" || !agent.Enabled {
		t.Fatalf("incorrect saved identity: %+v", agent)
	}
	saved, err := s.GetAgent(ctx, agent.ID)
	if err != nil || saved != agent {
		t.Fatalf("identity not saved: %+v %v", saved, err)
	}
	members, err := s.ListMembers(ctx, room.ID)
	if err != nil || len(members) != 2 || (members[0].ID != agent.ID && members[1].ID != agent.ID) {
		t.Fatalf("membership not saved: %+v %v", members, err)
	}
	events, err := s.ListWorkspaceEvents(ctx, before[len(before)-1].ID)
	if err != nil || len(events) != 2 || events[0].Type != "agent.updated" || events[0].RoomID != "" || events[0].EntityID != agent.ID || events[1].Type != "membership.updated" || events[1].RoomID != room.ID || events[1].EntityID != agent.ID {
		t.Fatalf("incorrect committed events: %+v %v", events, err)
	}
	updated, err := s.GetRoom(ctx, room.ID)
	if err != nil || updated.DefaultAgentID != assistant.ID || updated.ActivitySeq != events[1].ID {
		t.Fatalf("incorrect room routing or cursor: %+v %v", updated, err)
	}
	messages, err := s.ListMessages(ctx, room.ID, "", 0)
	if err != nil || len(messages) != 0 {
		t.Fatalf("creation added a message: %+v %v", messages, err)
	}
	runs, err := s.ListRoomRuns(ctx, room.ID)
	if err != nil || len(runs) != 0 {
		t.Fatalf("creation submitted work: %+v %v", runs, err)
	}
}

func TestCreateRoomAgentFailureLeavesNoPartialRecords(t *testing.T) {
	for _, name := range []string{"missing room", "invalid agent", "disabled agent", "existing identity", "duplicate handle", "membership write", "membership event"} {
		t.Run(name, func(t *testing.T) {
			s, room, assistant := roomFixture(t)
			roomID := room.ID
			agent := Agent{Name: "Reviewer", Handle: "reviewer", Access: "read", Enabled: true}
			wantCode := "invalid_agent"
			switch name {
			case "missing room":
				roomID = "missing"
				wantCode = "not_found"
			case "invalid agent":
				agent.Handle = "not a handle"
			case "disabled agent":
				agent.Enabled = false
			case "existing identity":
				agent = assistant
				agent.Instructions = "Must not overwrite the original."
			case "duplicate handle":
				agent.Handle = assistant.Handle
				wantCode = "name_conflict"
			case "membership write":
				if _, err := s.db.Exec(`CREATE TRIGGER reject_member BEFORE INSERT ON memberships BEGIN SELECT RAISE(ABORT,'membership write failed'); END`); err != nil {
					t.Fatal(err)
				}
				wantCode = ""
			case "membership event":
				if _, err := s.db.Exec(`CREATE TRIGGER reject_member_event BEFORE INSERT ON workspace_events WHEN NEW.type='membership.updated' BEGIN SELECT RAISE(ABORT,'membership event failed'); END`); err != nil {
					t.Fatal(err)
				}
				wantCode = ""
			}
			agentsBefore, err := s.ListAgents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			eventsBefore, err := s.ListWorkspaceEvents(ctx, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateRoomAgent(ctx, roomID, agent)
			if err == nil {
				t.Fatal("creation unexpectedly succeeded")
			}
			if wantCode != "" {
				requireCode(t, err, wantCode)
			}
			agentsAfter, err := s.ListAgents(ctx)
			if err != nil || !reflect.DeepEqual(agentsBefore, agentsAfter) {
				t.Fatalf("failed creation changed agents: %+v %v", agentsAfter, err)
			}
			members, err := s.ListMembers(ctx, room.ID)
			if err != nil || len(members) != 1 || members[0] != assistant {
				t.Fatalf("failed creation changed membership: %+v %v", members, err)
			}
			eventsAfter, err := s.ListWorkspaceEvents(ctx, 0)
			if err != nil || !reflect.DeepEqual(eventsBefore, eventsAfter) {
				t.Fatalf("failed creation leaked events: %+v %v", eventsAfter, err)
			}
			updated, err := s.GetRoom(ctx, room.ID)
			if err != nil || updated.DefaultAgentID != assistant.ID || updated.ActivitySeq != eventsBefore[len(eventsBefore)-1].ID {
				t.Fatalf("failed creation changed room: %+v %v", updated, err)
			}
		})
	}
}
