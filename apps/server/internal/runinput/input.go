// Package runinput builds bounded model context and immutable evidence for
// remote execution and the local execution regression harness.
package runinput

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// ConversationReader supplies visible history without execution capabilities.
type ConversationReader interface {
	GetMessage(context.Context, string) (storage.Message, error)
	ContextMessages(context.Context, string, string, int64, int64) ([]storage.Message, error)
	ListAgents(context.Context) ([]storage.Agent, error)
}

// DocumentReader resolves the prepared document pinned by an admitted reference.
type DocumentReader interface {
	GetDocument(context.Context, string) (storage.Document, error)
}

// FileReader reads an exact immutable version; it cannot publish working files.
type FileReader interface {
	ReadFile(context.Context, string, string, string) ([]byte, error)
}
