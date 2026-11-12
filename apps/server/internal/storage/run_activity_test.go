package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func activityRun(t *testing.T) (*Store, Run) {
	t.Helper()
	s, room, agent := roomFixture(t)
	origin, err := s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: "activity-origin", Content: "Prepare notes", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	run := Run{ID: "activity-run", RoomID: room.ID, AgentID: agent.ID, AgentSnapshot: agent, ResourceID: room.ResourceID, OriginMessageID: origin.Message.ID, Status: "running", CreatedAt: now(), UpdatedAt: now()}
	if err := s.transaction(ctx, func(tx *sql.Tx) error { return createRun(ctx, sqlgen.New(tx), run) }); err != nil {
		t.Fatal(err)
	}
	return s, run
}

func TestRunActivitySeeksDisplayEventsWithoutDrainingRawHistory(t *testing.T) {
	s, run := activityRun(t)
	const total = 20000
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for seq := int64(1); seq <= total; seq++ {
			kind, text := "output", strings.Repeat("ORIGINAL_OUTPUT ", 16)
			if seq%40 == 0 {
				kind, text = "tool_call", fmt.Sprintf(`{"name":"read_file","arguments":{"path":"note-%d.md"}}`, seq)
			}
			if err := q.CreateRunEvent(ctx, sqlgen.CreateRunEventParams{RunID: run.ID, Seq: seq, Type: kind, Text: text, CreatedAt: run.CreatedAt}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := s.RunActivity(ctx, run.ID, RunActivityPosition{})
	if err != nil || len(latest.Items) != 50 || latest.Items[0].Seq != 18040 || latest.Items[49].Seq != total || !latest.HasOlder || latest.HasNewer || latest.Through != total {
		t.Fatal("recent window", latest, err)
	}
	empty, err := s.RunActivity(ctx, run.ID, RunActivityPosition{Before: 1, Through: &latest.Through})
	if err != nil || len(empty.Items) != 0 || empty.HasOlder || !empty.HasNewer {
		t.Fatal("empty beginning", empty, err)
	}
	empty, err = s.RunActivity(ctx, run.ID, RunActivityPosition{After: total, Through: &latest.Through})
	if err != nil || len(empty.Items) != 0 || !empty.HasOlder || empty.HasNewer {
		t.Fatal("empty end", empty, err)
	}
	preview, err := s.RunActivity(ctx, run.ID, RunActivityPosition{Limit: 6})
	if err != nil || len(preview.Items) != 6 || preview.Items[5].Detail != "note-20000.md" {
		t.Fatal("bounded Room preview", preview, err)
	}
	older, err := s.RunActivity(ctx, run.ID, RunActivityPosition{Before: latest.Items[0].Seq, Through: &latest.Through})
	if err != nil || len(older.Items) != 50 || older.Items[49].Seq != 18000 || !older.HasNewer {
		t.Fatal("older", older, err)
	}
	if _, err := s.AppendRunEvent(ctx, run.ID, "tool_call", `{"name":"write_file","arguments":"{\"path\":\"latest.md\"}"}`); err != nil {
		t.Fatal(err)
	}
	later, err := s.RunActivity(ctx, run.ID, RunActivityPosition{After: older.Items[49].Seq, Through: &latest.Through})
	if err != nil || len(later.Items) != 50 || later.Items[49].Seq != total || later.HasNewer {
		t.Fatal("frozen traversal included a later event", later, err)
	}
	fresh, err := s.RunActivity(ctx, run.ID, RunActivityPosition{Limit: 6})
	if err != nil || fresh.Items[5].Detail != "latest.md" || fresh.Through != total+1 {
		t.Fatal("latest", fresh, err)
	}
	original, err := s.ListRunEvents(ctx, run.ID, 0)
	if err != nil || len(original) != 500 || original[0].Text != strings.Repeat("ORIGINAL_OUTPUT ", 16) {
		t.Fatal("original log changed", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM run_events", total+1)
	raw, err := json.Marshal(latest)
	if err != nil || len(raw) > 128<<10 || strings.Contains(string(raw), "ORIGINAL_OUTPUT") {
		t.Fatal("display projection leaked raw output", len(raw), err)
	}
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT seq FROM run_events WHERE run_id=? AND type IN ('model','tool_call','status','error','completion') AND seq<=? ORDER BY seq DESC LIMIT 50`, run.ID, total)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "run_activity_window")
	}
	if err := rows.Err(); err != nil || !indexed {
		t.Fatal("activity window must seek its display-only index", err)
	}
	t.Logf("20,000 original events: latest window %d bytes / %d entries; Room preview %d entries", len(raw), len(latest.Items), len(preview.Items))
}

func TestRunActivityBoundsUntrustedDisplayTextAndPreservesOriginals(t *testing.T) {
	s, run := activityRun(t)
	text := fmt.Sprintf(`{"name":%q,"arguments":{"command":%q}}`, strings.Repeat("工具", 300), strings.Repeat("笔记\n", 1000))
	for i := 0; i < 51; i++ {
		if _, err := s.AppendRunEvent(ctx, run.ID, "tool_call", text); err != nil {
			t.Fatal(err)
		}
	}
	window, err := s.RunActivity(ctx, run.ID, RunActivityPosition{})
	if err != nil || len(window.Items) != 50 {
		t.Fatal(err)
	}
	for _, item := range window.Items {
		if utf8.RuneCountInString(item.Name) > 80 || utf8.RuneCountInString(item.Detail) > 160 || !utf8.ValidString(item.Detail) {
			t.Fatal("unbounded or invalid display text", item)
		}
	}
	raw, _ := json.Marshal(window)
	if len(raw)+1 > 128<<10 {
		t.Fatal("unbounded wire response", len(raw))
	}
	original, err := s.ListRunEvents(ctx, run.ID, 0)
	if err != nil || original[0].Text != text {
		t.Fatal("display trimming changed original event bytes", err)
	}
	for _, value := range []string{`{bad`, `{"name":"bash","arguments":"{bad"}`, `{"name":true}`, `{"name":"bash","arguments":{"command":false,"path":"notes.md"}}`} {
		if _, err := s.AppendRunEvent(ctx, run.ID, "tool_call", value); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := s.RunActivity(ctx, run.ID, RunActivityPosition{Limit: 4})
	if err != nil || recent.Items[1].Name != "bash" || recent.Items[3].Detail != "notes.md" {
		t.Fatal("malformed arguments lost actual tool attribution", recent, err)
	}
}
