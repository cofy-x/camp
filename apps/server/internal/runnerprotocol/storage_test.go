package runnerprotocol

import "testing"

func TestStorageObservationRejectsAmbiguousAndUnsafeReports(t *testing.T) {
	zero := int64(0)
	good := StorageHealth{Schema: 1, ObservedAt: "2026-11-01T07:00:00Z", Level: "ok", Warnings: []string{}, LogicalBytes: &zero, FreeBytes: &zero, Complete: true}
	if err := ValidateStorageHealth(&good); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*StorageHealth){
		"unknown warning":   func(v *StorageHealth) { v.Level = "notice"; v.Warnings = []string{"host-path-secret"} },
		"negative bytes":    func(v *StorageHealth) { n := int64(-1); v.LogicalBytes = &n },
		"partial success":   func(v *StorageHealth) { v.Complete = false },
		"silent warning":    func(v *StorageHealth) { v.Level = "warning" },
		"bad time":          func(v *StorageHealth) { v.ObservedAt = "yesterday" },
		"duplicate warning": func(v *StorageHealth) { v.Level = "warning"; v.Warnings = []string{"disk-free-low", "disk-free-low"} },
	} {
		t.Run(name, func(t *testing.T) {
			value := good
			change(&value)
			if ValidateStorageHealth(&value) == nil {
				t.Fatal("Accepted invalid report")
			}
		})
	}
	unavailable := StorageHealth{Schema: 1, ObservedAt: good.ObservedAt, Level: "notice", Warnings: []string{"observation-unavailable"}}
	if err := ValidateStorageHealth(&unavailable); err != nil {
		t.Fatal(err)
	}
}
