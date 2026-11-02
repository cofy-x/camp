package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/serverstorage"
)

// Offline maintenance bypasses Server configuration and never loads model keys,
// starts a listener, migrates data or starts a Runner/service.
func runServerStorage(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		_, err := fmt.Fprintln(output, `Usage: camp storage inspect|verify|plan|apply|restore|purge|cancel|status --data-dir PATH [options]

Stop the Server first. Registered originals, versions, documents, task inputs,
receipts and Runner recovery remain protected. No age-based expiration runs.

  inspect          Read a bounded space/reference inventory without changing data
  verify           Also verify stopped SQLite and retained Git revision integrity
  plan             Persist a quarantine plan for up to --limit 50 orphan objects
  apply            Quarantine the exact --plan ID; repeat to reconcile interruption
  restore          Restore that plan without overwriting existing paths
  cancel           Restore a partial quarantine and cancel that plan
  purge            Permanently remove quarantined bytes; requires --confirm-permanent
  status           Read one --plan ID, or list retained maintenance receipts

Inspection copies at most 1 GiB of database/WAL to private temporary storage.
Inventory is bounded to 100,000 entries and depth 64. Plans contain at most
50 objects, 512 MiB of bytes and 32 MiB of metadata. Incomplete scans refuse
maintenance. --data-dir must be explicit; no private configuration is loaded.
Runner checkpoints use camp runner checkpoint storage and cleanup instead.`)
		return err
	}
	action := args[0]
	flags := flag.NewFlagSet("storage "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory, id string
	var permanent bool
	limit := 50
	flags.StringVar(&directory, "data-dir", "", "stopped Server directory")
	flags.StringVar(&id, "plan", "", "exact maintenance plan")
	flags.IntVar(&limit, "limit", 50, "maximum plan objects")
	flags.BoolVar(&permanent, "confirm-permanent", false, "confirm irreversible purge")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || directory == "" {
		return errors.New("Specify --data-dir PATH for a stopped Server")
	}
	if action != "purge" && permanent {
		return errors.New("Permanent confirmation is accepted only for purge")
	}
	if action != "plan" && limit != 50 {
		return errors.New("The object limit is accepted only for plan")
	}
	if (action == "inspect" || action == "verify" || action == "plan") && id != "" {
		return errors.New("This action does not accept a prior plan")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result any
	var err error
	switch action {
	case "inspect", "verify":
		result, err = serverstorage.Inspect(ctx, directory, action == "verify")
	case "plan":
		result, err = serverstorage.Plan(ctx, directory, limit)
	case "apply", "restore", "cancel", "purge":
		if id == "" {
			return errors.New("Specify the exact --plan ID")
		}
		if action == "apply" {
			action = "quarantine"
		}
		result, err = serverstorage.Apply(ctx, directory, id, action, permanent)
	case "status":
		result, err = serverstorage.Status(ctx, directory, id)
	default:
		return errors.New("Unknown Server storage command")
	}
	if err != nil {
		_ = json.NewEncoder(output).Encode(map[string]any{"complete": false, "error": err.Error(), "observation": result})
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
