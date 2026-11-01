package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

const storageObservationInterval = 5 * time.Minute

// The monitor joins before the executor closes. Sampling never holds the
// transport/lease loop and an unavailable observation never changes Ready.
func (d *Daemon) observeStorage(ctx context.Context) {
	if d.native == nil || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return
	}
	timer := time.NewTicker(storageObservationInterval)
	defer timer.Stop()
	for {
		sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		observed, err := d.native.StorageHealth(sampleCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			observed = &p.StorageHealth{Schema: 1, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Level: "notice", Warnings: []string{"observation-unavailable"}}
		}
		d.mu.Lock()
		d.storage = observed
		d.mu.Unlock()
		_ = writeStorageObservation(d.config, observed)
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

type storedStorageObservation struct {
	RunnerID  string           `json:"runnerId"`
	StorageID string           `json:"storageId"`
	Storage   *p.StorageHealth `json:"storage"`
}

func writeStorageObservation(config Config, value *p.StorageHealth) error {
	data, err := json.Marshal(storedStorageObservation{RunnerID: config.RunnerID, StorageID: config.StorageID, Storage: value})
	if err != nil {
		return err
	}
	return writePrivate(filepath.Join(config.DataDir, "storage-observation.json"), data)
}
