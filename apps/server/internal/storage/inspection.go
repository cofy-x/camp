package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
	"github.com/pressly/goose/v3"
)

const InspectionReferences = 100000

// InspectionCatalog contains identities and pins, never private configuration,
// credentials, original document text or task/message contents.
type InspectionCatalog struct {
	Identity  string              `json:"identity"`
	Resources map[string][]string `json:"-"`
	Reserved  map[string]bool     `json:"-"`
	Transfers map[string]string   `json:"-"`
	Inputs    int64               `json:"inputs"`
	Runs      int64               `json:"runs"`
	Tasks     int64               `json:"tasks"`
	Runners   int64               `json:"runners"`
}

// ReadInspectionCatalog opens an isolated stopped copy. It never migrates or
// repairs the selected database; the caller owns and deletes the private copy.
func ReadInspectionCatalog(ctx context.Context, filename string) (out InspectionCatalog, err error) {
	params := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "foreign_keys(1)"}}
	target := url.URL{Scheme: "file", Path: filename, RawQuery: params.Encode()}
	db, err := sql.Open("sqlite", target.String())
	if err != nil {
		return out, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var userVersion int
	if err = db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return out, err
	}
	if userVersion != 0 {
		return out, errors.New("Unsupported database version marker")
	}
	var identity, version int
	if err = db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&identity); err != nil {
		return out, err
	}
	if identity != databaseApplicationID {
		return out, errors.New("The database is not a supported Camp Server")
	}
	if err = db.QueryRowContext(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		return out, err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return out, err
	}
	latest := 0
	sources := make([]*goose.Source, 0, len(entries))
	for _, entry := range entries {
		n, e := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if e != nil {
			return out, e
		}
		latest = max(latest, n)
		sources = append(sources, &goose.Source{Version: int64(n)})
	}
	if version != latest {
		return out, fmt.Errorf("Use the matching Server to upgrade and stop this database before maintenance (schema %d, expected %d)", version, latest)
	}
	if err = checkMigrationHistory(ctx, db, sources); err != nil {
		return out, err
	}
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check(1)").Scan(&check); err != nil {
		return out, err
	}
	if check != "ok" {
		return out, errors.New("The stopped database failed its integrity check")
	}
	var broken int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM (SELECT 1 FROM pragma_foreign_key_check LIMIT 1)").Scan(&broken); err != nil {
		return out, err
	}
	if broken != 0 {
		return out, errors.New("The stopped database has broken references")
	}
	q := sqlgen.New(db)
	out.Identity, err = q.InspectionIdentity(ctx)
	if err != nil {
		return out, err
	}
	out.Resources = map[string][]string{}
	out.Reserved = map[string]bool{}
	out.Transfers = map[string]string{}
	resources, err := q.InspectionResources(ctx)
	if err != nil {
		return out, err
	}
	reservations, err := q.InspectionReservations(ctx)
	if err != nil {
		return out, err
	}
	transfers, err := q.InspectionTransfers(ctx)
	if err != nil {
		return out, err
	}
	revisions, err := q.InspectionRevisions(ctx)
	if err != nil {
		return out, err
	}
	if len(resources) > InspectionReferences || len(reservations) > InspectionReferences || len(transfers) > InspectionReferences || len(revisions) > InspectionReferences {
		return out, errors.New("The stopped reference catalog exceeds the inspection budget")
	}
	for _, r := range resources {
		out.Resources[r.ID] = []string{}
	}
	for _, id := range reservations {
		out.Reserved[id] = true
	}
	for _, r := range transfers {
		before, ok := out.Transfers[r.ID]
		if ok && !inspectionTerminal(before) {
			continue
		}
		out.Transfers[r.ID] = r.Status
	}
	for _, r := range revisions {
		if _, ok := out.Resources[r.ResourceID]; !ok {
			return out, errors.New("A retained version refers to a missing Resource")
		}
		out.Resources[r.ResourceID] = append(out.Resources[r.ResourceID], r.CommitID)
	}
	totals, err := q.InspectionTotals(ctx)
	if err != nil {
		return out, err
	}
	out.Inputs, out.Runs, out.Tasks, out.Runners = totals.TaskInputs, totals.Runs, totals.Tasks, totals.Runners
	return out, nil
}

func inspectionTerminal(status string) bool {
	switch status {
	case "finished", "succeeded", "failed", "cancelled", "interrupted":
		return true
	}
	return false
}
