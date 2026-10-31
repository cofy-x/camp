package layerfs

import (
	"context"
	"errors"
	"regexp"
)

// TreeCheckpoint is a complete private working-tree receipt. It is not a
// workspace.Archive, Server publication, authorization lease or durable Store.
type TreeCheckpoint struct {
	OperationID      string  `json:"operationId"`
	WorkspaceID      string  `json:"workspaceId"`
	AttemptID        string  `json:"attemptId"`
	Generation       int64   `json:"generation"`
	Digest           string  `json:"digest"`
	ParentCheckpoint *string `json:"parentCheckpoint"`
	Checkpoint       *string `json:"checkpoint"`
	StoreID          string  `json:"storeId"`
	CaptureRoot      string  `json:"captureRoot"`
	State            string  `json:"state"`
	Disposable       bool    `json:"disposable"`
	DurableDelivery  bool    `json:"durableDelivery"`
}

func (b *Bridge) CaptureTree(ctx context.Context, directory, workspaceID, attemptID string) (TreeCheckpoint, error) {
	var result TreeCheckpoint
	err := b.call(ctx, "worker.capture", map[string]any{"directory": directory, "workspaceId": workspaceID, "attemptId": attemptID, "quiescent": true}, &result, false)
	if err == nil && (result.WorkspaceID != workspaceID || result.AttemptID != attemptID || !result.valid() || !bridgePathInside(b.dataRoot, result.CaptureRoot)) {
		err = errors.New("The worker did not return a matching complete-tree checkpoint receipt")
	}
	return result, err
}

func (b *Bridge) CheckpointSelfTest(ctx context.Context) (TreeCheckpoint, error) {
	var result TreeCheckpoint
	err := b.call(ctx, "worker.selftest", nil, &result, false)
	if err == nil && !result.valid() {
		err = errors.New("The worker self-test returned no verified checkpoint")
	}
	return result, err
}

type PublicationView struct {
	Directory    string `json:"directory"`
	Digest       string `json:"digest"`
	SourceDigest string `json:"sourceDigest"`
	OperationID  string `json:"operationId"`
}

func (b *Bridge) CheckpointWorkspace(ctx context.Context, workspaceID string) error {
	var result struct {
		Ready bool `json:"ready"`
	}
	err := b.call(ctx, "worker.check", map[string]string{"workspaceId": workspaceID}, &result, false)
	if err == nil && !result.Ready {
		return errors.New("The native workspace has no ready checkpoint lineage")
	}
	return err
}

func (b *Bridge) PublicationView(ctx context.Context, checkpoint TreeCheckpoint, excluded []string) (PublicationView, error) {
	var result PublicationView
	err := b.call(ctx, "worker.publication", map[string]any{"operationId": checkpoint.OperationID, "excluded": excluded}, &result, false)
	if err == nil && (result.OperationID != checkpoint.OperationID || result.SourceDigest != checkpoint.Digest || result.Directory == "") {
		err = errors.New("The publication view differs from its confirmed complete-tree checkpoint")
	}
	return result, err
}

func (r TreeCheckpoint) valid() bool {
	return r.State == "confirmed" && r.Generation > 0 && regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Digest) && r.OperationID != "" && r.StoreID != "" && r.CaptureRoot != "" && r.Disposable && !r.DurableDelivery && (r.Checkpoint == nil || regexp.MustCompile(`^12[a-f0-9]{64}$`).MatchString(*r.Checkpoint))
}
