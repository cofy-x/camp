package daemon

import (
	"context"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"testing"
)

func TestCoordinationJournalSurvivesRestartWithoutReexecuting(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	journal, err := openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	command := p.Command{ID: "command", OperationID: "attempt", Kind: "run.start"}
	if _, err = journal.accept(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err = journal.begin(ctx, "attempt"); err != nil {
		t.Fatal(err)
	}
	intent := p.CoordinationIntent{CallID: "call", Kind: "finish", Summary: "Done", ArtifactPaths: []string{}}
	if err = journal.saveIntent(ctx, "attempt", intent); err != nil {
		t.Fatal(err)
	}
	if err = journal.saveIntent(ctx, "attempt", intent); err != nil {
		t.Fatal("identical call must replay", err)
	}
	changed := intent
	changed.Summary = "Different"
	if journal.saveIntent(ctx, "attempt", changed) == nil {
		t.Fatal("changed decision accepted")
	}
	if err = journal.db.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err = openJournal(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.db.Close()
	ops, err := journal.operations(ctx)
	if err != nil || len(ops) != 1 || ops[0].State != "running" || ops[0].Result != nil {
		t.Fatal("intent alone became an uploadable result", ops, err)
	}
	if begun, err := journal.begin(ctx, "attempt"); err != nil || begun {
		t.Fatal("restart replayed original prompt", begun, err)
	}
	if err = journal.cancel(ctx, "attempt"); err != nil {
		t.Fatal(err)
	}
	if journal.saveIntent(ctx, "attempt", intent) == nil {
		t.Fatal("cancelled execution accepted a decision")
	}
}
