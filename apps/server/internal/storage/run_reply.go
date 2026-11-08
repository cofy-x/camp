package storage

import (
	"context"
	"database/sql"
	"errors"
)

// GetRunReply selects the original Agent reply by its Run and Room identities.
// A Run may not yet have a reply; validating that identity never loads output.
func (s *Store) GetRunReply(ctx context.Context, id string) (*Message, error) {
	if _, err := s.queries().GetRunIdentity(ctx, id); err != nil {
		return nil, missing(err, "The run")
	}
	row, err := s.queries().GetRunReply(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	message, err := messageRow(row)
	if err != nil {
		return nil, err
	}
	messages := []Message{message}
	if err = s.messageWorkOrigins(ctx, messages); err != nil {
		return nil, err
	}
	return &messages[0], nil
}
