package storage

import "context"

// RunState contains only the identity and current state needed to present
// workspace Agent availability. Original execution snapshots remain lazy.
type RunState struct {
	ID      string
	AgentID string
	Status  string
}

// ListRunStates retains queued-before-active order so an active execution takes
// precedence over its Agent's later queued requests, across all Rooms.
func (s *Store) ListRunStates(ctx context.Context) ([]RunState, error) {
	rows, err := s.queries().ListRunStates(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]RunState, 0, len(rows))
	for _, row := range rows {
		states = append(states, RunState{ID: row.ID, AgentID: row.AgentID, Status: row.Status})
	}
	return states, nil
}
