package storage

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationTaskAdmissionRecordsSystemMandateAndFrozenEvidence(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	first := sendAutomationDiscussion(t, s, actor, room, "Prepare the first part of the new document.")
	second := sendAutomationDiscussion(t, s, actor, room, "Include the second part in the same request.")
	candidate := claimAutomation(t, s)
	assessment := orchestration.Assessment{Outcome: "accept", Goal: "Prepare both parts of the document.", Reason: "Both messages describe the same new goal."}
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	records := admissionRecords(t, s, room.ID)
	if len(records) != 3 {
		t.Fatal("automatic admission is missing from the ordinary admission journal", records)
	}
	record := records[2]
	trigger := record.Trigger
	if trigger.Source != "task" || trigger.Kind != orchestration.TaskStart || trigger.ActorKind != "system" || trigger.ActorID != "automation" || trigger.Request != nil || trigger.TaskID != task.ID || trigger.MessageID != task.OriginMessageID || trigger.InputRevision != 2 {
		t.Fatal("system admission impersonated a member or lost its work reference", trigger)
	}
	ref := trigger.Automation
	if ref == nil || ref.PolicyID != policy.ID || ref.PolicyRevision != policy.Revision || ref.EvaluationID != candidate.ID || !slices.Equal(ref.SourceEventIDs, []string{"room:" + first.Message.ID, "room:" + second.Message.ID}) {
		t.Fatal("automatic admission lost its frozen delegation and canonical evidence", ref)
	}
	if record.Decision.PolicyVersion != orchestration.AutomationPolicyVersion || record.Decision.Outcome != "accept" || len(record.Decision.Work) != 1 || len(record.Mandates) != 1 {
		t.Fatal("automatic admission did not retain one accepted task mandate", record)
	}
	mandate := record.Mandates[0]
	if mandate.Work.ID != task.ID || mandate.Work.Revision != task.Revision || mandate.RequestedByUserID != task.RequestedByUserID || mandate.ResourceID != task.ResourceID || mandate.BaseCommit != task.BaseSharedCommit || mandate.MaxRounds != policy.MaxTurns || mandate.MaxModelRequests != policy.MaxModelRequests || mandate.MaxTokens != task.Budget.MaxTokens || mandate.MaxSteps != task.Budget.MaxSteps || mandate.TimeoutSeconds != task.Budget.TimeoutSeconds || len(mandate.Participants) != 1 {
		t.Fatal("journal mandate differs from the frozen task grant", mandate, task)
	}
	participant := mandate.Participants[0]
	if participant.AgentID != writer.ID || participant.AgentRevision != writer.Revision || participant.RunnerID != task.Participants[0].RunnerID || participant.Access != writer.Access {
		t.Fatal("journal mandate lost the frozen Agent/Runner grant", participant)
	}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	if replay := admissionRecords(t, s, room.ID); len(replay) != len(records) || replay[2].Trigger.EventID != trigger.EventID {
		t.Fatal("completion replay appended another automatic mandate", replay)
	}
}

func TestAutomationMandateFailureRollsBackTaskAdmission(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "A task must not commit without its mandate.")
	candidate := claimAutomation(t, s)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_system_mandate BEFORE INSERT ON orchestration_records WHEN json_extract(NEW.value,'$.trigger.actorKind')='system' BEGIN SELECT RAISE(ABORT,'forced mandate failure'); END`); err != nil {
		t.Fatal(err)
	}
	assessment := orchestration.Assessment{Outcome: "accept", Goal: "Prepare the requested document.", Reason: "Within the saved delegation."}
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err == nil {
		t.Fatal("task admission committed despite journal failure")
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM runs", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_subscriptions", 0)
	assertScalar(t, s, "SELECT count(*) FROM messages WHERE task_id<>''", 0)
	assertScalar(t, s, "SELECT sum(tasks) FROM automation_usage", 0)
	row, err := s.queries().GetAutomationEvaluation(ctx, candidate.ID)
	if err != nil || row.Status != "running" {
		t.Fatal("failed transaction changed the retained assessment", row.Status, err)
	}
	if _, err = s.db.Exec("DROP TRIGGER reject_system_mandate"); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 1)
}

func TestAutomationJournalMigrationPreservesLegacyRecordsAndConstrainsSystemAdmissions(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 24)); err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, actor, "legacy")
	room, writer := remoteTestRoom(t, s, actor, runner)
	manual := startTask(t, s, actor, room, writer)
	var before string
	var sequence int64
	if err := s.db.QueryRow("SELECT seq,value FROM orchestration_records").Scan(&sequence, &before); err != nil {
		t.Fatal(err)
	}
	var receipt string
	if err := s.db.QueryRow("SELECT result FROM task_requests WHERE json_extract(result,'$.id')=?", manual.ID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE orchestration_records SET task_id='missing-task',value=json_set(value,'$.trigger.taskId','missing-task')"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err == nil {
		t.Fatal("a corrupt historical task reference passed migration")
	}
	assertScalar(t, s, "SELECT max(version_id) FROM goose_db_version", 24)
	assertScalar(t, s, "SELECT count(*) FROM sqlite_master WHERE name='orchestration_records_next'", 0)
	assertScalar(t, s, "SELECT count(*) FROM orchestration_records WHERE task_id='missing-task'", 1)
	if _, err := s.db.Exec("UPDATE orchestration_records SET task_id=?,value=? WHERE seq=?", manual.ID, before, sequence); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var after, afterReceipt string
	var afterSequence int64
	if err := s.db.QueryRow("SELECT seq,value FROM orchestration_records").Scan(&afterSequence, &after); err != nil || before != after || sequence != afterSequence {
		t.Fatal("migration changed historical journal bytes or sequence", err)
	}
	if err := s.db.QueryRow("SELECT result FROM task_requests WHERE json_extract(result,'$.id')=?", manual.ID).Scan(&afterReceipt); err != nil || afterReceipt != receipt {
		t.Fatal("migration changed an old task receipt", err)
	}
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "A future automatic admission after migration.")
	candidate := claimAutomation(t, s)
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare the future document.", Reason: "New authorized work."}); err != nil {
		t.Fatal(err)
	}
	records := admissionRecords(t, s, room.ID)
	record := records[len(records)-1]
	if record.Seq <= sequence || record.Trigger.Automation == nil {
		t.Fatal("migration failed to retain order or permit future system evidence", record)
	}
	for _, mutate := range []func(*orchestration.Record){
		func(r *orchestration.Record) { r.Trigger.ActorID = "another-system" },
		func(r *orchestration.Record) { r.Trigger.Kind = orchestration.TaskCancel },
		func(r *orchestration.Record) { r.Trigger.Automation = nil },
		func(r *orchestration.Record) { r.Trigger.Automation.PolicyRevision = 0 },
		func(r *orchestration.Record) { r.Trigger.Automation.SourceEventIDs = []string{} },
	} {
		encoded, _ := json.Marshal(record)
		var changed orchestration.Record
		if err := json.Unmarshal(encoded, &changed); err != nil {
			t.Fatal(err)
		}
		mutate(&changed)
		encoded, _ = json.Marshal(changed)
		if _, err := s.db.Exec("UPDATE orchestration_records SET value=? WHERE seq=?", string(encoded), record.Seq); err == nil {
			t.Fatal("system admission bypassed its bounded provenance constraint", changed)
		}
	}
}
