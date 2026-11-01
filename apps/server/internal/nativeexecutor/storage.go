package nativeexecutor

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/hoststorage"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func (e *Executor) StorageHealth(ctx context.Context) (*p.StorageHealth, error) {
	input := map[string]any{"command": "sample", "dataRoot": e.config.DataDir, "runnerId": e.config.RunnerID, "storageId": e.config.StorageID}
	data, err := hoststorage.Call(ctx, e.config.NodePath, e.config.WorkerRuntimeDir, e.config.DataDir, input, e.config.Lock)
	if err != nil {
		return nil, err
	}
	var result p.StorageHealth
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if err = p.ValidateStorageHealth(&result); err != nil {
		return nil, err
	}
	return &result, nil
}
