package runs

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// PrepareRemoteInput reuses the same bounded conversation and immutable evidence
// rules for the daemon transport. It does not initialize a local executor.
func PrepareRemoteInput(ctx context.Context, store *storage.Store, files *resources.Manager, run storage.Run, conversation storage.Conversation, directory string) (prompt, references string, err error) {
	m := &Manager{store: store, resources: files}
	prompt, err = m.conversationPrompt(ctx, run, conversation)
	if err == nil {
		references, err = m.prepareReferences(ctx, run, directory)
	}
	return
}

func (c Config) ModelDefaults() configuration.Model { return c.modelDefaults() }
