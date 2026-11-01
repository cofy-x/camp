package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func conversationRuntimeKey(ctx context.Context, q *sqlgen.Queries, r Run) (string, error) {
	runnerKey := ""
	if r.RunnerID != "" {
		runner, err := q.GetRunner(ctx, r.RunnerID)
		if err != nil {
			return "", err
		}
		var capability struct {
			Fingerprint string `json:"fingerprint"`
		}
		if err := json.Unmarshal([]byte(runner.Capabilities), &capability); err != nil {
			return "", err
		}
		runnerKey = "\x00" + r.RunnerID + "\x00" + runner.StorageID + "\x00" + runner.Runtime + "\x00" + capability.Fingerprint
	}
	if r.TaskInputProtocol == 1 {
		runnerKey += "\x00task-inputs-v1"
	}
	hash := sha256.Sum256([]byte(r.RuntimeKey + "\x00" + string(r.Environment) + runnerKey))
	return hex.EncodeToString(hash[:]), nil
}

// The owning transaction reserves the session before any expensive input I/O.
func prepareConversation(ctx context.Context, q *sqlgen.Queries, r Run) (Conversation, error) {
	old, err := q.GetConversation(ctx, sqlgen.GetConversationParams{AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r)})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, err
	}
	key, err := conversationRuntimeKey(ctx, q, r)
	if err != nil {
		return Conversation{}, err
	}
	out := Conversation{RuntimeKey: key, ID: old.ID, AgentID: r.AgentID, RoomID: r.RoomID, ThreadRootID: conversationScope(r), ResourceID: r.ResourceID, AgentRevision: r.AgentSnapshot.Revision, Generation: old.Generation, ContextSeq: old.ContextSeq, SessionID: old.SessionID, Resumable: old.Resumable}
	if out.ID == "" {
		out.ID = newID()
	}
	if !old.Resumable || old.AgentRevision != out.AgentRevision || old.ResourceID != r.ResourceID || old.RuntimeKey != key {
		out.Generation++
		out.ContextSeq = 0
		out.SessionID = "camp-" + newID()
		out.Resumable = false
	}
	err = q.SaveConversation(ctx, sqlgen.SaveConversationParams{RuntimeKey: key, ID: out.ID, AgentID: out.AgentID, RoomID: out.RoomID, ThreadRootID: out.ThreadRootID, ResourceID: out.ResourceID, AgentRevision: out.AgentRevision, Generation: out.Generation, ContextSeq: out.ContextSeq, SessionID: out.SessionID, Resumable: false})
	return out, err
}
