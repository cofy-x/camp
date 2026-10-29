package runs

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/layerfs"
)

const idleLayerProjects = 2
const maxPreparedUses = 8

type preparedUsage struct {
	active    int
	used      uint64
	completed int
}

// Allocation and idle eviction share a lifecycle lock. It never guards model
// execution or the scheduler; multiple workspaces can use the same baseline.
func (m *Manager) acquirePreparedProject(ctx context.Context, request layerfs.PrepareRequest) (layerfs.PreparedProject, error) {
	m.layerMu.Lock()
	defer m.layerMu.Unlock()
	prepared, err := m.bridge.Prepare(ctx, request)
	if err != nil {
		m.mu.Lock()
		m.unavailable = "LayerFS preparation failed. Recheck execution readiness to reconcile disposable resources before starting more work."
		m.mu.Unlock()
		return prepared, err
	}
	if m.layerProjects == nil {
		m.layerProjects = map[string]*preparedUsage{}
	}
	usage := m.layerProjects[prepared.ProjectID]
	if usage == nil {
		usage = &preparedUsage{}
		m.layerProjects[prepared.ProjectID] = usage
	}
	usage.active++
	m.layerClock++
	usage.used = m.layerClock
	return prepared, nil
}

func (m *Manager) releasePreparedProject(ctx context.Context, id string) error {
	m.layerMu.Lock()
	defer m.layerMu.Unlock()
	if usage := m.layerProjects[id]; usage != nil {
		if usage.active > 0 {
			usage.active--
			usage.completed++
		}
		m.layerClock++
		usage.used = m.layerClock
	}
	return m.prunePreparedProjects(ctx, m.bridge.RemoveProject)
}

// The public SDK also refuses removal if any incomplete or active workspace
// receipt exists. Failed cleanup therefore retains the entry and fails closed.
func (m *Manager) prunePreparedProjects(ctx context.Context, remove func(context.Context, string) error) error {
	for {
		idle, oldest, expired := 0, "", ""
		for id, usage := range m.layerProjects {
			if usage.active == 0 {
				idle++
				if usage.completed >= maxPreparedUses {
					expired = id
				}
				if oldest == "" || usage.used < m.layerProjects[oldest].used {
					oldest = id
				}
			}
		}
		if idle <= idleLayerProjects && expired == "" {
			return nil
		}
		if expired != "" {
			oldest = expired
		}
		if err := remove(ctx, oldest); err != nil {
			return err
		}
		delete(m.layerProjects, oldest)
	}
}
