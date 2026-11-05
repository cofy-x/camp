package orchestration

import (
	"strings"
	"unicode/utf8"
)

// ValidAssessment validates advisory data only. Storage still owns current
// identity, state, evidence, budget and authorization checks at commit.
func ValidAssessment(a Assessment) bool {
	text := func(s string, max int, empty bool) bool {
		return utf8.ValidString(s) && len(s) <= max && !strings.ContainsRune(s, 0) && (empty || strings.TrimSpace(s) != "")
	}
	if !text(a.Reason, 4096, false) || !text(a.Goal, 8192, true) || !text(a.CompletionCriteria, 8192, true) || !text(a.WorkItemID, 128, true) || !text(a.WaitCondition, 8192, true) || !text(a.Question, 8192, true) || !text(a.ResumeAt, 64, true) {
		return false
	}
	switch a.Outcome {
	case "accept", "execute":
		return a.WorkItemID != "" || text(a.Goal, 8192, false)
	case "wait":
		return text(a.WaitCondition, 8192, false) && (a.WorkItemID != "" || text(a.Goal, 8192, false))
	case "request_judgment":
		return text(a.Question, 8192, false) && (a.WorkItemID != "" || text(a.Goal, 8192, false))
	case "complete":
		return a.WorkItemID != ""
	case "ignore":
		return a.Goal == "" && a.WaitCondition == "" && a.Question == "" && a.ResumeAt == ""
	case "unavailable":
		return true
	default:
		return false
	}
}
