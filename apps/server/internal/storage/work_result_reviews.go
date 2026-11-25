package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type ResultFileReview struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Revision   int64  `json:"revision"`
	NextAction string `json:"nextAction,omitempty"`
}

type WorkResultReviews struct {
	CanRequest     bool                        `json:"canRequest"`
	CurrentVersion bool                        `json:"currentVersion"`
	Reviews        map[string]ResultFileReview `json:"reviews"`
}

// ReadWorkResultReviews reads the current result's exact file versions. The
// existing Review commands still own eligibility, decisions and deliveries.
func (s *Store) ReadWorkResultReviews(ctx context.Context, roomID, workID, resourceID, commit string) (out WorkResultReviews, err error) {
	if len(commit) != 40 || !commitValid(commit) || !validText(resourceID, 128, false) {
		return out, problem("invalid_reference", "Choose the exact Work result source and version.")
	}
	out.Reviews = map[string]ResultFileReview{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, err := reviewActor(ctx, q)
		if err != nil {
			return err
		}
		work, err := q.GetWorkItem(ctx, workID)
		if err != nil {
			return missing(err, "The work item")
		}
		if work.RoomID != roomID {
			return missing(sql.ErrNoRows, "The work item")
		}
		raw, err := q.WorkResultView(ctx, workID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var result orchestration.WorkResult
		if err == nil {
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				return err
			}
		}
		if !result.Published || result.ResourceID != resourceID || result.Commit != commit {
			return problem("revision_conflict", "The published Work result changed. Open its current version.")
		}
		room, err := q.GetRoom(ctx, roomID)
		if err != nil {
			return missing(err, "The Room")
		}
		resource, err := q.GetResource(ctx, resourceID)
		if err != nil {
			return missing(err, "The resource")
		}
		// A published task targets a working resource. A Room can since have
		// changed sources, but existing reviews of its old result stay readable.
		out.CurrentVersion = resource.CurrentCommit == commit
		out.CanRequest = room.ResourceID == resourceID && out.CurrentVersion
		paths := make([]string, 0, len(result.Files))
		for _, file := range result.Files {
			if file.Status != "D" && file.Status != "deleted" && validReferencePath(file.Path) {
				paths = append(paths, file.Path)
			}
		}
		if len(paths) == 0 {
			return nil
		}
		encoded, err := json.Marshal(paths)
		if err != nil {
			return err
		}
		rows, err := q.ResultFileReviews(ctx, sqlgen.ResultFileReviewsParams{Paths: string(encoded), RoomID: roomID, ResourceID: resourceID, CommitID: commit})
		if err != nil {
			return err
		}
		for _, row := range rows {
			review := ResultFileReview{ID: row.ID, Status: row.Status, Revision: row.Revision}
			if out.CurrentVersion {
				switch {
				case row.Status == "in_review" && row.ReviewerUserID == actor.ID:
					review.NextAction = "review"
				case row.Status == "approved" && (row.RequesterUserID == actor.ID || actor.Role == "admin"):
					review.NextAction = "deliver"
				}
			}
			out.Reviews[row.Path] = review
		}
		return nil
	})
	return
}
