package runnerprotocol

import (
	"errors"
	"time"
)

// StorageHealth is advisory and independent of execution readiness. It contains
// no host paths, credentials, worker endpoints or cleanup authority.
type StorageHealth struct {
	Schema              int      `json:"schema"`
	ObservedAt          string   `json:"observedAt"`
	Level               string   `json:"level"`
	Warnings            []string `json:"warnings"`
	LogicalBytes        *int64   `json:"logicalBytes"`
	Complete            bool     `json:"complete"`
	FreeBytes           *int64   `json:"freeBytes"`
	RetainedBudgetBytes *int64   `json:"retainedBudgetBytes"`
	MinFreeBytes        *int64   `json:"minFreeBytes"`
}

func ValidateStorageHealth(value *StorageHealth) error {
	if value == nil {
		return nil
	}
	if value.Schema != 1 || (value.Level != "ok" && value.Level != "notice" && value.Level != "warning") || len(value.Warnings) > 5 {
		return errors.New("Invalid storage observation format")
	}
	if _, err := time.Parse(time.RFC3339Nano, value.ObservedAt); err != nil {
		return errors.New("Invalid storage observation time")
	}
	for _, v := range []*int64{value.LogicalBytes, value.FreeBytes, value.RetainedBudgetBytes, value.MinFreeBytes} {
		if v != nil && (*v < 0 || *v > 1<<53-1) {
			return errors.New("Invalid storage byte observation")
		}
	}
	known := map[string]bool{"retained-budget-exceeded": true, "retained-budget-near": true, "disk-free-low": true, "observation-incomplete": true, "observation-unavailable": true}
	seen := map[string]bool{}
	for _, warning := range value.Warnings {
		if !known[warning] || seen[warning] {
			return errors.New("Invalid storage warning")
		}
		seen[warning] = true
	}
	if value.Complete && value.LogicalBytes == nil || value.Level == "ok" && (!value.Complete || value.FreeBytes == nil || len(value.Warnings) != 0) || value.Level != "ok" && len(value.Warnings) == 0 {
		return errors.New("Inconsistent storage observation")
	}
	return nil
}
