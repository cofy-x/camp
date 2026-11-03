package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
	"modernc.org/sqlite"
)

// Observe completed queries on a test-local Connector. The callback runs before
// database/sql returns the connection, without process-global driver hooks.
type observedReadConnector struct {
	driver.Connector
	afterQuery func(string)
}

func (c observedReadConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return observedReadConn{Conn: conn, afterQuery: c.afterQuery}, nil
}

type observedReadConn struct {
	driver.Conn
	afterQuery func(string)
}

func (c observedReadConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c observedReadConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c observedReadConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &observedReadRows{Rows: rows, afterClose: func() { c.afterQuery(query) }}, nil
}

type observedReadRows struct {
	driver.Rows
	afterClose func()
	once       sync.Once
}

func (r *observedReadRows) Close() error {
	err := r.Rows.Close()
	r.once.Do(r.afterClose)
	return err
}

func observedReadStore(t *testing.T, afterQuery func(string)) *Store {
	t.Helper()
	connector, err := sqlite.NewConnector(databaseDSN(filepath.Join(t.TempDir(), "snapshot.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(observedReadConnector{Connector: connector, afterQuery: afterQuery})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSourceImportListCannotCombineOldAdminAuthorityWithNewPrivateData(t *testing.T) {
	var armed atomic.Bool
	var authorityReads atomic.Int32
	reached, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	s := observedReadStore(t, func(query string) {
		if armed.Load() && strings.Contains(query, "-- name: GetUser :one") && authorityReads.Add(1) == 2 {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	admin, member := teamUsers(t, s)
	owner, _ := UserFromContext(member)
	reader, _ := UserFromContext(admin)
	adminRole := "admin"
	if _, err := s.UpdateUserAccess(admin, owner.ID, &adminRole, nil); err != nil {
		t.Fatal(err)
	}
	runner, _ := personalRunner(t, s, member, "alpha")
	original, err := s.CreateSourceImport(member, SourceImportInput{RequestID: newID(), RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "v1", RoomName: "Original private import"})
	if err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	readCtx, cancel := context.WithTimeout(admin, 5*time.Second)
	defer cancel()
	type readResult struct {
		items []SourceImport
		err   error
	}
	read := make(chan readResult, 1)
	go func() {
		items, err := s.ListSourceImports(readCtx)
		read <- readResult{items, err}
	}()
	select {
	case <-reached:
	case <-readCtx.Done():
		t.Fatal("source read did not reach its final authority lookup")
	}
	waits := s.db.Stats().WaitCount
	privateID := newID()
	write := make(chan error, 1)
	go func() {
		write <- s.transaction(readCtx, func(tx *sql.Tx) error {
			q := sqlgen.New(tx)
			if err := q.UpdateUserAccess(readCtx, sqlgen.UpdateUserAccessParams{ID: reader.ID, Role: "member", Enabled: true, UpdatedAt: now()}); err != nil {
				return err
			}
			return q.CreateSourceImport(readCtx, sqlgen.CreateSourceImportParams{ID: privateID, RunnerID: runner.ID, RequestedByUserID: owner.ID, RequestID: "new-after-demotion", RequestHash: strings.Repeat("a", 64), Source: "project", SourceRevision: "v1", SourceAlias: "Project", RoomName: "New private import", ResourceKey: newID(), CommandID: newID(), CreatedAt: now(), UpdatedAt: now()})
		})
	}()
	// Queue the writer before the last authority lookup returns. Without one
	// read transaction it wins the released connection before the data query.
	for s.db.Stats().WaitCount == waits {
		select {
		case <-readCtx.Done():
			t.Fatal("writer did not queue behind the authority read")
		default:
			runtime.Gosched()
		}
	}
	unblock.Do(func() { close(release) })
	result := <-read
	if err := <-write; err != nil {
		t.Fatal(err)
	}
	if result.err != nil || len(result.items) != 1 || result.items[0].ID != original.ID {
		t.Fatal("a pre-demotion admin read observed post-demotion private data", result.items, result.err)
	}
	items, err := s.ListSourceImports(admin)
	if err != nil || len(items) != 0 {
		t.Fatal("the stale request context retained administrator access", items, err)
	}
}

func TestSourceImportListAppliesVisibilityBeforeLimit(t *testing.T) {
	s := testStore(t)
	admin, member := teamUsers(t, s)
	ownedRunner, _ := personalRunner(t, s, member, "alpha")
	otherRunner, _ := personalRunner(t, s, admin, "bravo")
	actor, _ := UserFromContext(admin)
	original, err := s.CreateSourceImport(member, SourceImportInput{RequestID: newID(), RunnerID: ownedRunner.ID, SourceID: "project", ExpectedSourceRevision: "v1", RoomName: "Older owned import"})
	if err != nil {
		t.Fatal(err)
	}
	// More recent imports belonging to someone else must not consume the
	// member's entire visible page. Insert them together to avoid incidental IO.
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for i := 0; i < 201; i++ {
			if err := q.CreateSourceImport(ctx, sqlgen.CreateSourceImportParams{ID: newID(), RunnerID: otherRunner.ID, RequestedByUserID: actor.ID, RequestID: newID(), RequestHash: strings.Repeat("a", 64), Source: "project", SourceRevision: "v1", SourceAlias: "Project", RoomName: "Another owner's import", ResourceKey: newID(), CommandID: newID(), CreatedAt: "9999-01-01T00:00:00Z", UpdatedAt: now()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListSourceImports(member)
	if err != nil || len(items) != 1 || items[0].ID != original.ID {
		t.Fatal("other owners' imports hid an authorized import", items, err)
	}
	for _, scope := range []context.Context{ctx, admin} {
		items, err := s.ListSourceImports(scope)
		if err != nil || len(items) != 100 {
			t.Fatal("trusted and administrator listings must retain their existing limit", len(items), err)
		}
	}
}

func TestRunnerGrantListCannotCombineOldAdminAuthorityWithNewPrivateData(t *testing.T) {
	var armed atomic.Bool
	var authorityReads atomic.Int32
	reached, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	s := observedReadStore(t, func(query string) {
		if armed.Load() && strings.Contains(query, "-- name: GetUser :one") && authorityReads.Add(1) == 2 {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	admin, member := teamUsers(t, s)
	owner, _ := UserFromContext(member)
	reader, _ := UserFromContext(admin)
	adminRole := "admin"
	if _, err := s.UpdateUserAccess(admin, owner.ID, &adminRole, nil); err != nil {
		t.Fatal(err)
	}
	runner, _ := personalRunner(t, s, member, "alpha")
	room, err := s.SeedRoom(member, Resource{ID: newID(), Name: "Team files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	readCtx, cancel := context.WithTimeout(admin, 5*time.Second)
	defer cancel()
	type readResult struct {
		items []string
		err   error
	}
	read := make(chan readResult, 1)
	go func() {
		items, err := s.ListRunnerAgentGrants(readCtx, runner.ID)
		read <- readResult{items, err}
	}()
	select {
	case <-reached:
	case <-readCtx.Done():
		t.Fatal("grant read did not reach its final authority lookup")
	}
	waits := s.db.Stats().WaitCount
	write := make(chan error, 1)
	go func() {
		write <- s.transaction(readCtx, func(tx *sql.Tx) error {
			q := sqlgen.New(tx)
			if err := q.UpdateUserAccess(readCtx, sqlgen.UpdateUserAccessParams{ID: reader.ID, Role: "member", Enabled: true, UpdatedAt: now()}); err != nil {
				return err
			}
			return q.CreateRunnerAgentGrant(readCtx, sqlgen.CreateRunnerAgentGrantParams{RunnerID: runner.ID, AgentID: room.DefaultAgentID, CreatedAt: now()})
		})
	}()
	for s.db.Stats().WaitCount == waits {
		select {
		case <-readCtx.Done():
			t.Fatal("writer did not queue behind the authority read")
		default:
			runtime.Gosched()
		}
	}
	unblock.Do(func() { close(release) })
	result := <-read
	if err := <-write; err != nil {
		t.Fatal(err)
	}
	if result.err != nil || len(result.items) != 0 {
		t.Fatal("a pre-demotion admin read observed post-demotion private grants", result.items, result.err)
	}
	_, err = s.ListRunnerAgentGrants(admin, runner.ID)
	requireCode(t, err, "forbidden")
	items, err := s.ListRunnerAgentGrants(member, runner.ID)
	if err != nil || len(items) != 1 || items[0] != room.DefaultAgentID {
		t.Fatal("the owner must still see the new grant", items, err)
	}
}

func TestRunnerEnrollmentListCannotCombineOldAdminAuthorityWithNewPrivateData(t *testing.T) {
	var armed atomic.Bool
	var authorityReads atomic.Int32
	reached, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	s := observedReadStore(t, func(query string) {
		if armed.Load() && strings.Contains(query, "-- name: GetUser :one") && authorityReads.Add(1) == 2 {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() { unblock.Do(func() { close(release) }) })
	admin, member := teamUsers(t, s)
	owner, _ := UserFromContext(member)
	reader, _ := UserFromContext(admin)
	adminRole := "admin"
	if _, err := s.UpdateUserAccess(admin, owner.ID, &adminRole, nil); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	readCtx, cancel := context.WithTimeout(admin, 5*time.Second)
	defer cancel()
	type readResult struct {
		items []RunnerEnrollment
		err   error
	}
	read := make(chan readResult, 1)
	go func() {
		items, err := s.ListRunnerEnrollments(readCtx)
		read <- readResult{items, err}
	}()
	select {
	case <-reached:
	case <-readCtx.Done():
		t.Fatal("enrollment read did not reach its final authority lookup")
	}
	waits := s.db.Stats().WaitCount
	privateID := newID()
	write := make(chan error, 1)
	go func() {
		write <- s.transaction(readCtx, func(tx *sql.Tx) error {
			q := sqlgen.New(tx)
			if err := q.UpdateUserAccess(readCtx, sqlgen.UpdateUserAccessParams{ID: reader.ID, Role: "member", Enabled: true, UpdatedAt: now()}); err != nil {
				return err
			}
			if err := q.CreateRunnerEnrollment(readCtx, sqlgen.CreateRunnerEnrollmentParams{ID: privateID, Name: "New private enrollment", TokenHash: strings.Repeat("a", 64), ExpiresAt: remoteStamp(time.Now().Add(time.Hour)), CreatedAt: now()}); err != nil {
				return err
			}
			return q.SetEnrollmentOwner(readCtx, sqlgen.SetEnrollmentOwnerParams{ID: privateID, OwnerUserID: nullableParent(owner.ID)})
		})
	}()
	for s.db.Stats().WaitCount == waits {
		select {
		case <-readCtx.Done():
			t.Fatal("writer did not queue behind the authority read")
		default:
			runtime.Gosched()
		}
	}
	unblock.Do(func() { close(release) })
	result := <-read
	if err := <-write; err != nil {
		t.Fatal(err)
	}
	if result.err != nil || len(result.items) != 0 {
		t.Fatal("a pre-demotion admin read observed post-demotion private enrollments", result.items, result.err)
	}
	items, err := s.ListRunnerEnrollments(admin)
	if err != nil || len(items) != 0 {
		t.Fatal("the stale request context retained administrator access", items, err)
	}
	items, err = s.ListRunnerEnrollments(member)
	if err != nil || len(items) != 1 || items[0].ID != privateID {
		t.Fatal("the owner must still see the new enrollment", items, err)
	}
}
