package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type streamTiming struct {
	sync.Mutex
	recording    atomic.Bool
	transactions []time.Duration
}
type streamConnector struct {
	driver.Connector
	timing *streamTiming
}

func (c streamConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return streamConnection{Conn: conn, timing: c.timing}, nil
}

type streamConnection struct {
	driver.Conn
	timing *streamTiming
}

func (c streamConnection) Begin() (driver.Tx, error) {
	start := time.Now()
	tx, err := c.Conn.Begin()
	if err != nil {
		return nil, err
	}
	return streamTransaction{Tx: tx, timing: c.timing, start: start}, nil
}
func (c streamConnection) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	start := time.Now()
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err := conn.BeginTx(ctx, opts)
		if err != nil {
			return nil, err
		}
		return streamTransaction{Tx: tx, timing: c.timing, start: start}, nil
	}
	return c.Begin()
}
func (c streamConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if conn, ok := c.Conn.(driver.QueryerContext); ok {
		return conn.QueryContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}
func (c streamConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if conn, ok := c.Conn.(driver.ExecerContext); ok {
		return conn.ExecContext(ctx, query, args)
	}
	return nil, driver.ErrSkip
}

type streamTransaction struct {
	driver.Tx
	timing *streamTiming
	start  time.Time
}

func (tx streamTransaction) Commit() error {
	err := tx.Tx.Commit()
	if tx.timing.recording.Load() {
		tx.timing.Lock()
		tx.timing.transactions = append(tx.timing.transactions, time.Since(tx.start))
		tx.timing.Unlock()
	}
	return err
}

func streamPercentile(values []time.Duration, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	values = slices.Clone(values)
	slices.Sort(values)
	return float64(values[(len(values)-1)*p/100].Microseconds()) / 1000
}

// This opt-in fixture measures real SQLite ingestion and concurrent display
// reads, without a Runner process, provider call or inherited configuration.
func TestRunStreamQualification(t *testing.T) {
	if os.Getenv("CAMP_STREAM_QUALIFY") != "1" {
		t.Skip("set CAMP_STREAM_QUALIFY=1 for isolated storage measurements")
	}
	for _, streams := range []int{1, 4} {
		for _, initial := range []int{0, MaxRunOutputSize} {
			t.Run(fmt.Sprintf("streams=%d/output=%d", streams, initial), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "stream.sqlite")
				connector, err := sqlite.NewConnector(databaseDSN(path))
				if err != nil {
					t.Fatal(err)
				}
				timing := &streamTiming{}
				db := sql.OpenDB(streamConnector{Connector: connector, timing: timing})
				db.SetMaxOpenConns(1)
				s := &Store{db: db}
				t.Cleanup(func() { s.Close() })
				if err = s.migrate(ctx); err != nil {
					t.Fatal(err)
				}
				owner, _ := teamUsers(t, s)
				base, err := s.SeedRoom(owner, Resource{ID: "stream-files", Name: "Stream files", CurrentCommit: strings.Repeat("a", 40)})
				if err != nil {
					t.Fatal(err)
				}
				attempts := []Attempt{}
				runs := []Run{}
				snapshotBytes := 0
				for i := 0; i < streams; i++ {
					runner, _ := enrollTestRunner(t, s, owner, fmt.Sprintf("s%d", i))
					room, err := s.CreateRoom(owner, Room{Name: fmt.Sprintf("Stream %d", i), ResourceID: base.ResourceID})
					if err != nil {
						t.Fatal(err)
					}
					room, err = s.SetRoomRunner(owner, room.ID, runner.ID, room.RunnerRevision)
					if err != nil {
						t.Fatal(err)
					}
					agent, err := s.CreateRoomAgent(owner, room.ID, Agent{Name: fmt.Sprintf("Reader %d", i), Handle: fmt.Sprintf("reader-%d", i), Access: "read", Enabled: true, PreferredRunnerID: runner.ID})
					if err != nil {
						t.Fatal(err)
					}
					queued := sendRemoteTestRun(t, s, owner, room, agent, fmt.Sprintf("stream-%d", i))
					run, attempt, err := s.ClaimRemoteRun(ctx, queued.ID, queued.RunnerID, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					command, _ := json.Marshal(map[string]string{"id": attempt.CommandID, "operationId": attempt.ID, "kind": "run.start"})
					if err = s.SaveAttemptCommand(ctx, attempt.ID, command, fmt.Sprintf("%064x", i+1)); err != nil {
						t.Fatal(err)
					}
					if _, err = s.AcceptAttempt(ctx, attempt.ID, run.RunnerID); err != nil {
						t.Fatal(err)
					}
					run.Output = strings.Repeat("x", initial)
					run.AgentSnapshot.Instructions = strings.Repeat("Frozen instruction. ", 3200)
					run.Prompt = strings.Repeat("Brief. ", 1000)
					if err = saveRun(ctx, s.queries(), run); err != nil {
						t.Fatal(err)
					}
					raw, _ := json.Marshal(run)
					snapshotBytes = len(raw)
					attempts = append(attempts, attempt)
					runs = append(runs, run)
				}
				if _, err = db.Exec("PRAGMA wal_autocheckpoint=0"); err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
					t.Fatal(err)
				}
				const events = 200
				chunk := strings.Repeat("streamed output\n", 128)
				before := db.Stats()
				stop := make(chan struct{})
				var reader sync.WaitGroup
				latencies := []time.Duration{}
				readErrors := []error{}
				reader.Add(1)
				go func() {
					defer reader.Done()
					ticker := time.NewTicker(2 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							start := time.Now()
							_, err := s.GetRoom(ctx, base.ID)
							latencies = append(latencies, time.Since(start))
							if err != nil {
								readErrors = append(readErrors, err)
							}
						}
					}
				}()
				var writers sync.WaitGroup
				failures := make(chan error, streams)
				timing.recording.Store(true)
				start := time.Now()
				for i, attempt := range attempts {
					writers.Add(1)
					go func(i int, attempt Attempt) {
						defer writers.Done()
						for seq := 1; seq <= events; seq++ {
							_, err := s.RecordAttemptEvent(ctx, runs[i].RunnerID, attempt.ID, int64(seq), "assistant", chunk, strings.Repeat("f", 64))
							if err != nil {
								failures <- err
								return
							}
						}
					}(i, attempt)
				}
				writers.Wait()
				elapsed := time.Since(start)
				timing.recording.Store(false)
				close(stop)
				reader.Wait()
				close(failures)
				for err := range failures {
					t.Fatal(err)
				}
				for _, err := range readErrors {
					t.Fatal(err)
				}
				after := db.Stats()
				wal, err := os.Stat(path + "-wal")
				if err != nil {
					t.Fatal(err)
				}
				timing.Lock()
				transactions := slices.Clone(timing.transactions)
				timing.Unlock()
				result := map[string]any{"streams": streams, "initialOutputBytes": initial, "fullRunBytes": snapshotBytes, "events": streams * events, "chunkBytes": len(chunk), "elapsedMs": float64(elapsed.Microseconds()) / 1000, "transactionP50Ms": streamPercentile(transactions, 50), "transactionP95Ms": streamPercentile(transactions, 95), "readP95Ms": streamPercentile(latencies, 95), "connectionWaitMs": float64((after.WaitDuration - before.WaitDuration).Microseconds()) / 1000, "connectionWaits": after.WaitCount - before.WaitCount, "walBytes": wal.Size()}
				raw, _ := json.Marshal(result)
				t.Log(string(raw))
				for i, run := range runs {
					want := run.Output
					for range events {
						want = AppendOutputTail(want, chunk)
					}
					got, err := s.GetRun(ctx, run.ID)
					if err != nil || got.Output != want || got.Prompt != run.Prompt || got.AgentSnapshot.Instructions != run.AgentSnapshot.Instructions {
						t.Fatal("stream composition changed original facts", i, err)
					}
				}
			})
		}
	}
}
