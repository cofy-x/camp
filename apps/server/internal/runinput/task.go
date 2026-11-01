package runinput

import (
	"fmt"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// TaskBrief never truncates the original goal or explicit task inputs. General
// room history is separately bounded by the existing context builder.
func TaskBrief(t storage.CollaborationTask, r storage.Run) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Camp collaboration %s\nOriginal goal:\n%s\n\nCurrent handoff:\n%s\n\nApproved participants:\n", t.ID, t.Goal, r.Prompt)
	for _, p := range t.Participants {
		fmt.Fprintf(&b, "- %s (%s), %s access\n", p.Agent.Name, p.Agent.ID, p.Agent.Access)
	}
	b.WriteString("\nTask inputs to process this round:\n")
	end := min(int(r.TaskInputRevision), len(t.Inputs))
	for _, i := range t.Inputs[:end] {
		fmt.Fprintf(&b, "%s: %s\n", i.AuthorName, i.Content)
	}
	b.WriteString("\nYou must end this round with exactly one Camp coordination tool: camp_handoff, camp_ask_person, or camp_finish. Ordinary @mentions do not dispatch work. Working files are a task draft until final publication. A read-only reviewer must hand changed drafts back to a writer for final checks.\n")
	return b.String()
}
