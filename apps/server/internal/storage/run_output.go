package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const runOutputChunkBytes = 4096
const runOutputMarker = "[Earlier output was truncated; retention is limited to 256 KiB]\n"

func coldRunJSON(run Run) ([]byte, error) {
	// Output has a separate owner. Shadow the embedded wire field so snapshots
	// cannot accidentally retain a second, stale copy of the streaming tail.
	return json.Marshal(struct {
		Run
		Output *string `json:"output,omitempty"`
	}{Run: run})
}
func createRun(ctx context.Context, q *sqlgen.Queries, run Run) error {
	raw, err := coldRunJSON(run)
	if err != nil {
		return err
	}
	if err = q.CreateRun(ctx, sqlgen.CreateRunParams{ID: run.ID, RoomID: run.RoomID, ThreadRootID: nullableParent(run.ThreadRootID), OriginMessageID: run.OriginMessageID, AgentID: run.AgentID, ResourceID: run.ResourceID, Status: run.Status, Snapshot: string(raw), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt, RequestedByUserID: run.RequestedByUserID}); err != nil {
		return err
	}
	if err = q.InitRunProgress(ctx, sqlgen.InitRunProgressParams{RunID: run.ID, UpdatedAt: run.UpdatedAt}); err != nil {
		return err
	}
	if run.Output != "" {
		return replaceRunOutput(ctx, q, run.ID, run.Output, run.UpdatedAt)
	}
	return nil
}

func outputPiece(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}
func replaceRunOutput(ctx context.Context, q *sqlgen.Queries, run, output, stamp string) error {
	if err := q.ClearRunOutput(ctx, run); err != nil {
		return err
	}
	size := len(output)
	for seq := int64(1); len(output) > 0; seq++ {
		piece := outputPiece(output, runOutputChunkBytes)
		if err := q.InsertRunOutputChunk(ctx, sqlgen.InsertRunOutputChunkParams{RunID: run, Seq: seq, Text: piece}); err != nil {
			return err
		}
		output = output[len(piece):]
	}
	return q.SaveRunProgress(ctx, sqlgen.SaveRunProgressParams{RunID: run, UpdatedAt: stamp, OutputBytes: int64(size), OutputPrefix: ""})
}

// Append only touches a small tail chunk and, on overflow, the head boundary.
// Removing this derived display tail never deletes original events/receipts.
func appendRunOutput(ctx context.Context, q *sqlgen.Queries, run, addition, stamp string) error {
	addition = strings.ToValidUTF8(addition, "�")
	if strings.ContainsRune(addition, 0) {
		return problem("invalid_run", "Run output contains a null character.")
	}
	if len(addition) > MaxRunOutputSize {
		return replaceRunOutput(ctx, q, run, AppendOutputTail("", addition), stamp)
	}
	progress, err := q.GetRunProgress(ctx, run)
	if err != nil {
		return err
	}
	size := progress.OutputBytes + int64(len(addition))
	tail, err := q.LastRunOutputChunk(ctx, run)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && len(tail.Text) < runOutputChunkBytes && len(addition) > 0 {
		piece := outputPiece(addition, runOutputChunkBytes-len(tail.Text))
		if len(piece) > 0 {
			if err = q.SaveRunOutputChunk(ctx, sqlgen.SaveRunOutputChunkParams{RunID: run, Seq: tail.Seq, Text: tail.Text + piece}); err != nil {
				return err
			}
			addition = addition[len(piece):]
		}
	}
	for len(addition) > 0 {
		tail.Seq++
		piece := outputPiece(addition, runOutputChunkBytes)
		if err = q.InsertRunOutputChunk(ctx, sqlgen.InsertRunOutputChunkParams{RunID: run, Seq: tail.Seq, Text: piece}); err != nil {
			return err
		}
		addition = addition[len(piece):]
	}
	prefix := progress.OutputPrefix
	if size > MaxRunOutputSize {
		target := int64(MaxRunOutputSize - len(runOutputMarker) - 3)
		remove := size - int64(len(prefix)) - target
		size = int64(len(runOutputMarker)) + target
		prefix = runOutputMarker
		for remove > 0 {
			head, err := q.FirstRunOutputChunk(ctx, run)
			if err != nil {
				return err
			}
			if remove >= int64(len(head.Text)) {
				if err = q.DeleteRunOutputChunk(ctx, sqlgen.DeleteRunOutputChunkParams{RunID: run, Seq: head.Seq}); err != nil {
					return err
				}
				remove -= int64(len(head.Text))
			} else {
				trimmed := strings.ToValidUTF8(head.Text[remove:], "�")
				size += int64(len(trimmed)) - (int64(len(head.Text)) - remove)
				if err = q.SaveRunOutputChunk(ctx, sqlgen.SaveRunOutputChunkParams{RunID: run, Seq: head.Seq, Text: trimmed}); err != nil {
					return err
				}
				remove = 0
			}
		}
	}
	return q.SaveRunProgress(ctx, sqlgen.SaveRunProgressParams{RunID: run, UpdatedAt: stamp, OutputBytes: size, OutputPrefix: prefix})
}

// AppendOutputTail keeps the established UTF-8 display-tail contract for in-memory readers.
func AppendOutputTail(old, addition string) string {
	text := strings.ToValidUTF8(old+addition, "�")
	if len(text) <= MaxRunOutputSize {
		return text
	}
	return runOutputMarker + strings.ToValidUTF8(text[len(text)-(MaxRunOutputSize-len(runOutputMarker)-3):], "�")
}

type RunStreamUpdate struct {
	Status    string
	UpdatedAt string
}

// RecordRunOutput is the local executor's activity write. Finite state changes
// and terminal results retain their existing full validation/write owners.
func (s *Store) RecordRunOutput(ctx context.Context, id, kind, eventText, output string) (out RunStreamUpdate, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		state, err := q.GetRunStreamIdentity(ctx, id)
		if err != nil {
			return missing(err, "The run")
		}
		if runTerminal(state.Status) {
			return problem("run_finished", "This run has already finished.")
		}
		if _, err = appendRunEvent(ctx, q, id, kind, eventText); err != nil {
			return err
		}
		stamp := now()
		if err = appendRunOutput(ctx, q, id, output, stamp); err != nil {
			return err
		}
		out = RunStreamUpdate{Status: state.Status, UpdatedAt: stamp}
		return event(ctx, q, "run.activity", state.RoomID, id)
	})
	return out, err
}
