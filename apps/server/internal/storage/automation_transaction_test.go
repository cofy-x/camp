package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type automationQueryCounter struct {
	sqlgen.DBTX
	reads int
}

func TestAutomationRoutingKeepsDisabledSubscriptionScopeAndFencesItsRevision(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	thread := room.ID + ":room"
	subscription, err := s.SetTaskSubscription(actor, task.ID, orchestration.SubscriptionInput{RequestID: newID(), SourceID: "room", ThreadKey: thread, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	before, frontier, err := automationTasks(ctx, s.queries(), room.ID, "room", thread, nil)
	if err != nil || len(before) != 1 || !before[0].ReceivesInputs {
		t.Fatal("active matched subscription lost its task scope", before, err)
	}
	_, err = s.SetTaskSubscription(actor, task.ID, orchestration.SubscriptionInput{RequestID: newID(), Revision: subscription.Revision, SourceID: "room", ThreadKey: thread, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	after, changed, err := automationTasks(ctx, s.queries(), room.ID, "room", thread, nil)
	if err != nil || len(after) != 1 || after[0].ID != task.ID || after[0].Goal != task.Goal || after[0].ReceivesInputs || changed == frontier {
		t.Fatal("disabling a subscription omitted the old goal or failed to fence its frozen route", after, frontier, changed, err)
	}
}

func (c *automationQueryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	c.reads++
	return c.DBTX.QueryContext(ctx, query, args...)
}

func (c *automationQueryCounter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	c.reads++
	return c.DBTX.QueryRowContext(ctx, query, args...)
}

func TestAutomationRelatedTaskProjectionKeepsTerminalFrontierWithoutRoomHistoryQueries(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	related := startTask(t, s, actor, room, writer)
	unrelated := startTask(t, s, actor, room, writer)
	for index := range 8 {
		_, err := s.CreateTask(actor, room.ID, CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Goal: fmt.Sprintf("Unrelated goal %d", index), ThreadRootID: unrelated.ThreadRootID, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
		if err != nil {
			t.Fatal(err)
		}
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		related.Status = "completed"
		if err := saveTask(ctx, q, &related); err != nil {
			return err
		}
		counter := &automationQueryCounter{DBTX: tx}
		tasks, frontier, err := automationTasks(ctx, sqlgen.New(counter), room.ID, "room", room.ID+":"+related.ThreadRootID, nil)
		if err != nil {
			return err
		}
		if len(tasks) != 1 || tasks[0].ID != related.ID || tasks[0].Goal != related.Goal || tasks[0].Status != "completed" || tasks[0].ReceivesInputs {
			t.Fatalf("related terminal scope was omitted or unrelated work leaked: %+v", tasks)
		}
		// Existing in-flight assessments retain this exact routing fingerprint,
		// including an empty receipt list when none of their batch was delivered.
		expected, err := json.Marshal([]struct {
			ID                   string
			Goal                 string
			Status               string
			ReceivesInputs       bool
			SubscriptionID       string
			SubscriptionRevision int64
			SubscriptionEnabled  bool
			ReceivedEventIDs     []string
		}{{ID: related.ID, Goal: related.Goal, Status: "completed", ReceivedEventIDs: []string{}}})
		if err != nil {
			return err
		}
		if frontier != remoteHash(expected) {
			t.Fatal("routing projection changed the frozen assessment frontier", frontier, string(expected))
		}
		if counter.reads != 1 {
			t.Fatalf("routing performed %d SQL reads for one relevant task among unrelated history; want one projection", counter.reads)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
