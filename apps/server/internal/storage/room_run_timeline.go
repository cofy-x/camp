package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const RoomRunTimelineLimit = 20
const RoomRunTimelineBytes = 320 << 10

type RunTimelineAgent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
	Access string `json:"access"`
}
type RunTimelineFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
type RunTimelineCheck struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exitCode"`
}
type RunTimelineCheckName struct {
	Name string `json:"name"`
}
type RunTimelineEnvironment struct {
	Checks []RunTimelineCheckName `json:"checks"`
}

// RunTimeline contains only display facts. Execution inputs and original file
// bytes remain with the full Run and immutable resource readers.
type RunTimeline struct {
	ID                string                  `json:"id"`
	RoomID            string                  `json:"roomId"`
	ThreadRootID      string                  `json:"threadRootId"`
	OriginMessageID   string                  `json:"originMessageId"`
	AgentID           string                  `json:"agentId"`
	ResourceID        string                  `json:"resourceId"`
	TaskID            string                  `json:"taskId,omitempty"`
	RunnerID          string                  `json:"runnerId"`
	RunnerName        string                  `json:"runnerName"`
	Status            string                  `json:"status"`
	CreatedAt         string                  `json:"createdAt"`
	UpdatedAt         string                  `json:"updatedAt"`
	FinishedAt        string                  `json:"finishedAt"`
	SessionMode       string                  `json:"sessionMode"`
	SessionGeneration int64                   `json:"sessionGeneration"`
	WaitingReason     string                  `json:"waitingReason,omitempty"`
	Error             string                  `json:"error"`
	ResultCommit      string                  `json:"resultCommit"`
	Published         bool                    `json:"published"`
	ChecksDeferred    bool                    `json:"checksDeferred"`
	TestExitCode      *int                    `json:"testExitCode"`
	AgentSnapshot     RunTimelineAgent        `json:"agentSnapshot"`
	Environment       *RunTimelineEnvironment `json:"environment"`
	Checks            []RunTimelineCheck      `json:"checks"`
	Files             []RunTimelineFile       `json:"files"`
	FileCount         int                     `json:"fileCount"`
	FilesLimited      bool                    `json:"filesLimited"`
}
type roomRunTimelineCursor struct {
	Room      string `json:"room"`
	At        string `json:"at"`
	ID        string `json:"id"`
	ThroughAt string `json:"throughAt"`
	ThroughID string `json:"throughId"`
}
type RoomRunTimelinePage struct {
	Items      []RunTimeline `json:"items"`
	NextCursor string        `json:"nextCursor"`
	position   roomRunTimelineCursor
	more       bool
}

func timelineCursor(position roomRunTimelineCursor, last RunTimeline) string {
	position.At, position.ID = last.CreatedAt, last.ID
	data, _ := json.Marshal(position)
	return base64.RawURLEncoding.EncodeToString(data)
}

// Bound reapplies the wire bound after a Manager overlays a retained terminal
// result or current queue reason. The next cursor never skips a removed row.
func (page *RoomRunTimelinePage) Bound() error {
	more := page.more || page.NextCursor != ""
	all := page.Items
	page.Items = []RunTimeline{}
	page.NextCursor = ""
	for index, item := range all {
		for {
			single, err := json.Marshal(RoomRunTimelinePage{Items: []RunTimeline{item}, NextCursor: timelineCursor(page.position, item)})
			if err != nil {
				return err
			}
			if len(single)+1 <= RoomRunTimelineBytes {
				break
			}
			if len(item.Files) == 0 {
				return problem("invalid_page", "This execution summary cannot fit a bounded page. Open its full details.")
			}
			item.Files = item.Files[:len(item.Files)-1]
			item.FilesLimited = true
		}
		page.Items = append(page.Items, item)
		if more || index < len(all)-1 {
			page.NextCursor = timelineCursor(page.position, item)
		} else {
			page.NextCursor = ""
		}
		data, err := json.Marshal(page)
		if err != nil {
			return err
		}
		if len(data)+1 > RoomRunTimelineBytes {
			page.Items = page.Items[:len(page.Items)-1]
			if len(page.Items) == 0 {
				return problem("invalid_page", "This execution summary cannot fit a bounded page. Open its full details.")
			}
			page.NextCursor = timelineCursor(page.position, page.Items[len(page.Items)-1])
			return nil
		}
	}
	return nil
}
func (s *Store) RoomRunTimeline(ctx context.Context, room, cursor string, limit int) (RoomRunTimelinePage, error) {
	page := RoomRunTimelinePage{Items: []RunTimeline{}}
	if _, err := s.GetRoom(ctx, room); err != nil {
		return page, err
	}
	if limit < 0 || limit > RoomRunTimelineLimit {
		return page, problem("invalid_page", "Read at most 20 execution summaries at a time.")
	}
	if limit == 0 {
		limit = RoomRunTimelineLimit
	}
	position := roomRunTimelineCursor{Room: room}
	if cursor != "" {
		if len(cursor) > 1368 {
			return page, problem("invalid_page", "The execution cursor is too large.")
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(data) > 1024 || json.Unmarshal(data, &position) != nil || position.Room != room || !validText(position.ID, 128, false) || !validText(position.ThroughID, 128, false) || position.At > position.ThroughAt || (position.At == position.ThroughAt && position.ID > position.ThroughID) {
			return page, problem("invalid_page", "The execution cursor is invalid for this Room.")
		}
		if _, err = time.Parse(time.RFC3339Nano, position.At); err != nil {
			return page, problem("invalid_page", "The execution cursor is invalid.")
		}
		if _, err = time.Parse(time.RFC3339Nano, position.ThroughAt); err != nil {
			return page, problem("invalid_page", "The execution frontier is invalid.")
		}
	} else {
		last, err := s.queries().RoomRunTimelineFrontier(ctx, room)
		if errors.Is(err, sql.ErrNoRows) {
			return page, nil
		}
		if err != nil {
			return page, err
		}
		position.ThroughAt, position.ThroughID = last.CreatedAt, last.ID
	}
	page.position = position
	rows, err := s.queries().RoomRunTimelinePage(ctx, sqlgen.RoomRunTimelinePageParams{RoomID: room, AfterCreatedAt: position.At, AfterID: position.ID, ThroughCreatedAt: position.ThroughAt, ThroughID: position.ThroughID, PageLimit: int64(limit + 1)})
	if err != nil {
		return page, err
	}
	for index, row := range rows {
		if index == limit {
			page.more = true
			break
		}
		var item RunTimeline
		if err = json.Unmarshal([]byte(row), &item); err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	err = page.Bound()
	return page, err
}

// ProjectRunTimeline applies the same whitelist to an in-memory terminal result.
func ProjectRunTimeline(run Run) RunTimeline {
	out := RunTimeline{ID: run.ID, RoomID: run.RoomID, ThreadRootID: run.ThreadRootID, OriginMessageID: run.OriginMessageID, AgentID: run.AgentID, ResourceID: run.ResourceID, TaskID: run.TaskID, RunnerID: run.RunnerID, RunnerName: run.RunnerName, Status: run.Status, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt, FinishedAt: run.FinishedAt, SessionMode: run.SessionMode, SessionGeneration: run.SessionGeneration, WaitingReason: run.WaitingReason, Error: run.Error, ResultCommit: run.ResultCommit, Published: run.Published, ChecksDeferred: run.ChecksDeferred, TestExitCode: run.TestExitCode, AgentSnapshot: RunTimelineAgent{ID: run.AgentSnapshot.ID, Name: run.AgentSnapshot.Name, Avatar: run.AgentSnapshot.Avatar, Access: run.AgentSnapshot.Access}, Checks: []RunTimelineCheck{}, Files: []RunTimelineFile{}, FileCount: len(run.Files), FilesLimited: len(run.Files) > 20}
	if string(run.Environment) != "null" && len(run.Environment) > 0 {
		var environment RunTimelineEnvironment
		if json.Unmarshal(run.Environment, &environment) == nil {
			if environment.Checks == nil {
				environment.Checks = []RunTimelineCheckName{}
			}
			out.Environment = &environment
		}
	}
	if err := json.Unmarshal(run.Checks, &out.Checks); err != nil && len(run.Checks) > 0 {
		out.Checks = []RunTimelineCheck{{Status: "incomplete"}}
	}
	if out.Checks == nil {
		out.Checks = []RunTimelineCheck{}
	}
	if out.AgentSnapshot.ID == "" {
		out.AgentSnapshot.ID = run.AgentID
	}
	for index, file := range run.Files {
		if index == 20 {
			break
		}
		out.Files = append(out.Files, RunTimelineFile{Path: file.Path, Status: file.Status})
	}
	return out
}

// BoundRunSummary applies the same exact-file preview bound to one Run.
func BoundRunSummary(item RunTimeline) (RunTimeline, error) {
	page := RoomRunTimelinePage{Items: []RunTimeline{item}}
	if err := page.Bound(); err != nil {
		return RunTimeline{}, err
	}
	return page.Items[0], nil
}

// GetRunSummary reads whitelisted display facts without materializing original
// output, file bytes, Agent instructions or frozen execution configuration.
func (s *Store) GetRunSummary(ctx context.Context, id string) (RunTimeline, error) {
	raw, err := s.queries().GetRunSummary(ctx, id)
	if err != nil {
		return RunTimeline{}, missing(err, "The run")
	}
	var item RunTimeline
	if err = json.Unmarshal([]byte(raw), &item); err != nil {
		return item, err
	}
	return BoundRunSummary(item)
}
