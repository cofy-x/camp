package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Review records explicit human acceptance of one exact published file version.
// Discussion stays in the existing Room Thread; Agent output cannot decide it.
type Review struct {
	ID              string `json:"id"`
	RoomID          string `json:"roomId"`
	ResourceID      string `json:"resourceId"`
	Path            string `json:"path"`
	Title           string `json:"title"`
	RequesterUserID string `json:"requesterUserId"`
	RequesterName   string `json:"requesterName"`
	ReviewerUserID  string `json:"reviewerUserId"`
	ReviewerName    string `json:"reviewerName"`
	ReviewerEnabled bool   `json:"reviewerEnabled"`
	SubmittedCommit string `json:"submittedCommit"`
	DeliveredCommit string `json:"deliveredCommit"`
	Status          string `json:"status"`
	Revision        int64  `json:"revision"`
	ThreadRootID    string `json:"threadRootId"`
	Outdated        bool   `json:"outdated"`
	CurrentCommit   string `json:"currentCommit"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}
type ReviewEvent struct {
	ID             string `json:"id"`
	Seq            int64  `json:"seq"`
	ReviewID       string `json:"reviewId"`
	ActorUserID    string `json:"actorUserId"`
	ActorName      string `json:"actorName"`
	Action         string `json:"action"`
	Comment        string `json:"comment"`
	Commit         string `json:"commit"`
	ReviewerUserID string `json:"reviewerUserId"`
	Revision       int64  `json:"revision"`
	MessageID      string `json:"messageId"`
	CreatedAt      string `json:"createdAt"`
}
type ReviewResult struct {
	Review   Review        `json:"review"`
	Events   []ReviewEvent `json:"events"`
	Replayed bool          `json:"replayed"`
}
type CreateReviewInput struct {
	RequestID      string `json:"requestId"`
	Title          string `json:"title"`
	ResourceID     string `json:"resourceId"`
	Path           string `json:"path"`
	Commit         string `json:"commit"`
	ReviewerUserID string `json:"reviewerUserId"`
}
type ReviewActionInput struct {
	RequestID      string `json:"requestId"`
	Revision       int64  `json:"revision"`
	Action         string `json:"action"`
	Comment        string `json:"comment"`
	Commit         string `json:"commit"`
	ReviewerUserID string `json:"reviewerUserId"`
}

func reviewRow(r sqlgen.GetReviewRow) Review {
	return Review{ID: r.ID, RoomID: r.RoomID, ResourceID: r.ResourceID, Path: r.Path, Title: r.Title, RequesterUserID: r.RequesterUserID, RequesterName: r.RequesterName, ReviewerUserID: r.ReviewerUserID, ReviewerName: r.ReviewerName, ReviewerEnabled: r.ReviewerEnabled, SubmittedCommit: r.SubmittedCommit, DeliveredCommit: r.DeliveredCommit, Status: r.Status, Revision: r.Revision, ThreadRootID: r.ThreadRootID, CurrentCommit: r.CurrentCommit, Outdated: r.Status != "delivered" && r.Status != "cancelled" && r.SubmittedCommit != r.CurrentCommit, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func reviewEvents(ctx context.Context, q *sqlgen.Queries, id string, through int64) ([]ReviewEvent, error) {
	rows, err := q.ListReviewEvents(ctx, id)
	out := make([]ReviewEvent, 0, len(rows))
	for _, r := range rows {
		if through > 0 && r.Revision > through {
			break
		}
		out = append(out, ReviewEvent{ID: r.ID, Seq: r.Seq, ReviewID: r.ReviewID, ActorUserID: r.ActorUserID, ActorName: r.ActorName, Action: r.Action, Comment: r.Comment, Commit: r.CommitID, ReviewerUserID: r.ReviewerUserID, Revision: r.Revision, MessageID: r.MessageID, CreatedAt: r.CreatedAt})
	}
	return out, err
}
func getReview(ctx context.Context, q *sqlgen.Queries, id string) (Review, error) {
	r, err := q.GetReview(ctx, id)
	if err != nil {
		return Review{}, missing(err, "The review")
	}
	return reviewRow(r), nil
}
func (s *Store) GetReview(ctx context.Context, id string) (result ReviewResult, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		var e error
		result.Review, e = getReview(ctx, q, id)
		if e != nil {
			return e
		}
		result.Events, e = reviewEvents(ctx, q, id, 0)
		return e
	})
	return
}
func (s *Store) ListReviews(ctx context.Context, room, resource string) ([]Review, error) {
	if room != "" {
		if _, err := s.GetRoom(ctx, room); err != nil {
			return nil, err
		}
	}
	if resource != "" {
		if _, err := s.GetResource(ctx, resource); err != nil {
			return nil, err
		}
	}
	rows, err := s.queries().ListReviews(ctx, sqlgen.ListReviewsParams{RoomID: room, ResourceID: resource})
	out := make([]Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, reviewRow(sqlgen.GetReviewRow(r)))
	}
	return out, err
}
func reviewActor(ctx context.Context, q *sqlgen.Queries) (User, error) {
	u, ok := UserFromContext(ctx)
	if !ok {
		return User{}, problem("unauthenticated", "Sign in before changing a review.")
	}
	current, err := q.GetUser(ctx, u.ID)
	if err != nil || !current.Enabled {
		return User{}, problem("forbidden", "This account can no longer change reviews.")
	}
	return userRow(current), nil
}
func reviewFingerprint(operation, id string, input any) string {
	b, _ := json.Marshal(struct {
		Operation string
		ID        string
		Input     any
	}{operation, id, input})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func findReviewRequest(ctx context.Context, q *sqlgen.Queries, actor, room, request, hash string) (ReviewResult, bool, error) {
	r, err := q.GetReviewRequest(ctx, sqlgen.GetReviewRequestParams{UserID: actor, RoomID: room, RequestID: request})
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewResult{}, false, nil
	}
	if err != nil {
		return ReviewResult{}, false, err
	}
	if r.RequestHash != hash {
		return ReviewResult{}, false, problem("request_conflict", "This request ID belongs to a different review change.")
	}
	var out ReviewResult
	if err = json.Unmarshal([]byte(r.Result), &out.Review); err != nil {
		return ReviewResult{}, false, err
	}
	out.Events, err = reviewEvents(ctx, q, out.Review.ID, out.Review.Revision)
	out.Replayed = true
	return out, true, err
}
func saveReviewRequest(ctx context.Context, q *sqlgen.Queries, actor, request, hash string, out *ReviewResult) error {
	b, err := json.Marshal(out.Review)
	if err != nil {
		return err
	}
	if err = q.CreateReviewRequest(ctx, sqlgen.CreateReviewRequestParams{UserID: actor, RoomID: out.Review.RoomID, RequestID: request, RequestHash: hash, Result: string(b)}); err != nil {
		return err
	}
	out.Events, err = reviewEvents(ctx, q, out.Review.ID, 0)
	return err
}
func normalizeCreateReview(in CreateReviewInput) (CreateReviewInput, error) {
	in.RequestID = strings.TrimSpace(in.RequestID)
	in.Title = strings.TrimSpace(in.Title)
	if !validText(in.RequestID, 128, false) || !validText(in.Title, 200, false) || !validText(in.ReviewerUserID, 128, false) {
		return in, problem("invalid_review", "Provide a request ID, a title of at most 200 bytes, and a human reviewer.")
	}
	if err := validateReferenceInput(ReferenceInput{Kind: "file", ResourceID: in.ResourceID, Path: in.Path, Commit: in.Commit}); err != nil {
		return in, err
	}
	return in, nil
}

// FindCreateReview recovers a committed receipt before any file preflight.
func (s *Store) FindCreateReview(ctx context.Context, room string, in CreateReviewInput) (ReviewResult, bool, error) {
	in, err := normalizeCreateReview(in)
	if err != nil {
		return ReviewResult{}, false, err
	}
	return findReviewRequest(ctx, s.queries(), actorID(ctx), room, in.RequestID, reviewFingerprint("request", room, in))
}
func (s *Store) CreateReview(ctx context.Context, room string, in CreateReviewInput) (out ReviewResult, err error) {
	in, err = normalizeCreateReview(in)
	if err != nil {
		return
	}
	hash := reviewFingerprint("request", room, in)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, e := reviewActor(ctx, q)
		if e != nil {
			return e
		}
		previous, found, e := findReviewRequest(ctx, q, actor.ID, room, in.RequestID, hash)
		if e != nil {
			return e
		}
		if found {
			out = previous
			return nil
		}
		r, e := q.GetRoom(ctx, room)
		if e != nil {
			return missing(e, "The room")
		}
		if r.ResourceID != in.ResourceID {
			return problem("invalid_review", "Choose a file from this Room's current working resource.")
		}
		if e = requireWorkingResource(ctx, q, in.ResourceID); e != nil {
			return e
		}
		resource, e := validateResourceRevision(ctx, q, in.ResourceID, in.Commit)
		if e != nil {
			return e
		}
		if resource.CurrentCommit != in.Commit {
			return problem("revision_conflict", "Room files changed. Reload the current file before requesting review.")
		}
		reviewer, e := q.GetUser(ctx, in.ReviewerUserID)
		if e != nil || !reviewer.Enabled {
			return problem("invalid_review", "Choose an enabled human reviewer.")
		}
		stamp := now()
		review := Review{ID: newID(), RoomID: room, ResourceID: in.ResourceID, Path: in.Path, Title: in.Title, RequesterUserID: actor.ID, RequesterName: actor.Name, ReviewerUserID: reviewer.ID, ReviewerName: reviewer.Name, ReviewerEnabled: true, SubmittedCommit: in.Commit, CurrentCommit: in.Commit, Status: "in_review", Revision: 1, ThreadRootID: newID(), CreatedAt: stamp, UpdatedAt: stamp}
		message, e := reviewMessage(ctx, q, review, actor, "request", "", review.ThreadRootID)
		if e != nil {
			return e
		}
		if e = q.CreateReview(ctx, sqlgen.CreateReviewParams{ID: review.ID, RoomID: review.RoomID, ResourceID: review.ResourceID, Path: review.Path, Title: review.Title, RequesterUserID: review.RequesterUserID, ReviewerUserID: review.ReviewerUserID, SubmittedCommit: review.SubmittedCommit, Status: review.Status, Revision: review.Revision, ThreadRootID: review.ThreadRootID, CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		if e = recordReviewTransition(ctx, q, review, actor, "request", "", message); e != nil {
			return e
		}
		out.Review = review
		return saveReviewRequest(ctx, q, actor.ID, in.RequestID, hash, &out)
	})
	return
}
func normalizeReviewAction(in ReviewActionInput) (ReviewActionInput, error) {
	in.RequestID = strings.TrimSpace(in.RequestID)
	in.Comment = strings.TrimSpace(in.Comment)
	if !validText(in.RequestID, 128, false) || in.Revision < 1 || !validText(in.Comment, 8192, true) {
		return in, problem("invalid_review", "Include a request ID, the current review revision, and a comment of at most 8192 bytes.")
	}
	switch in.Action {
	case "approve", "deliver", "cancel":
	case "request_changes":
		if in.Comment == "" {
			return in, problem("invalid_review", "Describe the changes needed before requesting a revision.")
		}
	case "resubmit":
		if len(in.Commit) != 40 || !commitValid(in.Commit) {
			return in, problem("invalid_review", "Resubmit an exact published version.")
		}
	case "reassign":
		if !validText(in.ReviewerUserID, 128, false) {
			return in, problem("invalid_review", "Choose an enabled human reviewer.")
		}
	default:
		return in, problem("invalid_review", "Choose a supported review action.")
	}
	if in.Action != "resubmit" && in.Commit != "" || in.Action != "reassign" && in.ReviewerUserID != "" {
		return in, problem("invalid_review", "The review action includes unrelated fields.")
	}
	return in, nil
}
func (s *Store) FindReviewAction(ctx context.Context, id string, in ReviewActionInput) (ReviewResult, bool, error) {
	in, err := normalizeReviewAction(in)
	if err != nil {
		return ReviewResult{}, false, err
	}
	r, err := getReview(ctx, s.queries(), id)
	if err != nil {
		return ReviewResult{}, false, err
	}
	return findReviewRequest(ctx, s.queries(), actorID(ctx), r.RoomID, in.RequestID, reviewFingerprint("action", id, in))
}
func (s *Store) ActOnReview(ctx context.Context, id string, in ReviewActionInput) (out ReviewResult, err error) {
	in, err = normalizeReviewAction(in)
	if err != nil {
		return
	}
	hash := reviewFingerprint("action", id, in)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, e := reviewActor(ctx, q)
		if e != nil {
			return e
		}
		review, e := getReview(ctx, q, id)
		if e != nil {
			return e
		}
		previous, found, e := findReviewRequest(ctx, q, actor.ID, review.RoomID, in.RequestID, hash)
		if e != nil {
			return e
		}
		if found {
			out = previous
			return nil
		}
		if review.Revision != in.Revision {
			return problem("revision_conflict", "This review changed. Reload it before making a decision.")
		}
		if review.Status == "delivered" || review.Status == "cancelled" {
			return problem("review_finished", "This review is finished. Request a new review for another delivery.")
		}
		deciding := in.Action == "approve" || in.Action == "request_changes"
		if deciding {
			if actor.ID != review.ReviewerUserID {
				return problem("forbidden", "Only the assigned human reviewer can decide this review.")
			}
		} else if actor.ID != review.RequesterUserID && actor.Role != "admin" {
			return problem("forbidden", "Only the requester or an administrator can manage this review.")
		}
		switch in.Action {
		case "approve", "request_changes":
			if review.Status != "in_review" {
				return problem("review_conflict", "This version already has a decision. Resubmit it to request another review.")
			}
			if review.Outdated {
				return problem("revision_conflict", "Room files changed. Resubmit the current version before reviewing it.")
			}
			if in.Action == "approve" {
				review.Status = "approved"
			} else {
				review.Status = "changes_requested"
			}
		case "resubmit":
			if review.CurrentCommit != in.Commit {
				return problem("revision_conflict", "Room files changed. Reload the file before resubmitting it.")
			}
			if review.Status == "in_review" && review.SubmittedCommit == in.Commit {
				return problem("review_conflict", "This version is already awaiting review.")
			}
			if !review.ReviewerEnabled {
				return problem("review_conflict", "The reviewer is disabled. Assign an active reviewer before resubmitting.")
			}
			review.SubmittedCommit = in.Commit
			review.Status = "in_review"
			review.Outdated = false
		case "deliver":
			if review.Status != "approved" {
				return problem("review_conflict", "A human reviewer must approve this version before delivery.")
			}
			if review.Outdated {
				return problem("revision_conflict", "Room files changed after approval. Resubmit the current version before delivery.")
			}
			review.Status = "delivered"
			review.DeliveredCommit = review.SubmittedCommit
		case "cancel":
			review.Status = "cancelled"
			review.Outdated = false
		case "reassign":
			reviewer, e := q.GetUser(ctx, in.ReviewerUserID)
			if e != nil || !reviewer.Enabled {
				return problem("invalid_review", "Choose an enabled human reviewer.")
			}
			if reviewer.ID == review.ReviewerUserID && review.Status == "in_review" {
				return problem("review_conflict", "This reviewer is already assigned.")
			}
			review.ReviewerUserID = reviewer.ID
			review.ReviewerName = reviewer.Name
			review.ReviewerEnabled = true
			review.Status = "in_review"
		}
		review.Revision++
		review.UpdatedAt = now()
		count, e := q.UpdateReview(ctx, sqlgen.UpdateReviewParams{ReviewerUserID: review.ReviewerUserID, SubmittedCommit: review.SubmittedCommit, DeliveredCommit: review.DeliveredCommit, Status: review.Status, UpdatedAt: review.UpdatedAt, ID: review.ID, Revision: in.Revision})
		if e != nil {
			return e
		}
		if count != 1 {
			return problem("revision_conflict", "This review changed before the update could be saved.")
		}
		message, e := reviewMessage(ctx, q, review, actor, in.Action, in.Comment, "")
		if e != nil {
			return e
		}
		if e = recordReviewTransition(ctx, q, review, actor, in.Action, in.Comment, message); e != nil {
			return e
		}
		out.Review = review
		return saveReviewRequest(ctx, q, actor.ID, in.RequestID, hash, &out)
	})
	return
}
func reviewMessage(ctx context.Context, q *sqlgen.Queries, r Review, actor User, action, comment, rootID string) (Message, error) {
	var content string
	switch action {
	case "request":
		content = fmt.Sprintf("%s requested %s's review of %s.", actor.Name, r.ReviewerName, r.Title)
	case "approve":
		content = fmt.Sprintf("%s approved %s.", actor.Name, r.Title)
	case "request_changes":
		content = fmt.Sprintf("%s requested changes to %s.", actor.Name, r.Title)
	case "resubmit":
		content = fmt.Sprintf("%s submitted a version of %s for %s to review.", actor.Name, r.Title, r.ReviewerName)
	case "deliver":
		content = fmt.Sprintf("%s delivered the approved version of %s.", actor.Name, r.Title)
	case "cancel":
		content = fmt.Sprintf("%s cancelled the review of %s.", actor.Name, r.Title)
	case "reassign":
		content = fmt.Sprintf("%s assigned %s to review %s. A new decision is required.", actor.Name, r.ReviewerName, r.Title)
	}
	if comment != "" {
		content += "\n\n" + comment
	}
	seq, err := q.NextMessageSequence(ctx, r.RoomID)
	if err != nil {
		return Message{}, err
	}
	m := Message{ID: newID(), RoomID: r.RoomID, ReviewID: r.ID, ThreadRootID: r.ThreadRootID, Seq: seq, AuthorKind: "system", AuthorUserID: actor.ID, AuthorName: actor.Name, Content: content, CreatedAt: r.UpdatedAt, UpdatedAt: r.UpdatedAt, References: []Reference{{Kind: "file", ResourceID: r.ResourceID, Commit: r.SubmittedCommit, Path: r.Path, Title: r.Title}}}
	if rootID != "" {
		m.ID = rootID
		m.ThreadRootID = ""
	}
	return m, insertMessage(ctx, q, m, "")
}
func recordReviewTransition(ctx context.Context, q *sqlgen.Queries, r Review, actor User, action, comment string, m Message) error {
	if err := q.CreateReviewEvent(ctx, sqlgen.CreateReviewEventParams{ID: newID(), ReviewID: r.ID, ActorUserID: actor.ID, ActorName: actor.Name, Action: action, Comment: comment, CommitID: r.SubmittedCommit, ReviewerUserID: r.ReviewerUserID, Revision: r.Revision, MessageID: m.ID, CreatedAt: r.UpdatedAt}); err != nil {
		return err
	}
	recipients := []string{r.RequesterUserID, r.ReviewerUserID}
	kind := "review_updated"
	if action == "request" || action == "resubmit" || action == "reassign" {
		kind = "review_requested"
	}
	seen := map[string]bool{actor.ID: true}
	for _, id := range recipients {
		if seen[id] {
			continue
		}
		seen[id] = true
		u, err := q.GetUser(ctx, id)
		if err != nil {
			return err
		}
		if !u.Enabled {
			continue
		}
		if err = q.CreateInboxItem(ctx, sqlgen.CreateInboxItemParams{ID: newID(), UserID: id, Kind: kind, RoomID: r.RoomID, MessageID: m.ID, ReviewID: r.ID, CreatedAt: r.UpdatedAt}); err != nil {
			return err
		}
	}
	if err := event(ctx, q, "message.created", r.RoomID, m.ID); err != nil {
		return err
	}
	return event(ctx, q, "review.updated", r.RoomID, r.ID)
}
