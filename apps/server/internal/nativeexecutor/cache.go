package nativeexecutor

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
func (m *layerRuntime) acquirePreparedProject(ctx context.Context, request layerfs.PrepareRequest) (layerfs.PreparedProject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prepared, err := m.bridge.Prepare(ctx, request)
	if err != nil {
		m.unavailable = err
		return prepared, err
	}
	if m.projects == nil {
		m.projects = map[string]*preparedUsage{}
	}
	usage := m.projects[prepared.ProjectID]
	if usage == nil {
		usage = &preparedUsage{}
		m.projects[prepared.ProjectID] = usage
	}
	usage.active++
	m.clock++
	usage.used = m.clock
	return prepared, nil
}

func (m *layerRuntime) releasePreparedProject(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if usage := m.projects[id]; usage != nil {
		if usage.active > 0 {
			usage.active--
			usage.completed++
		}
		m.clock++
		usage.used = m.clock
	}
	return m.prunePreparedProjects(ctx, m.bridge.RemoveProject)
}

// The public SDK also refuses removal if any incomplete or active workspace
// receipt exists. Failed cleanup therefore retains the entry and fails closed.
func (m *layerRuntime) prunePreparedProjects(ctx context.Context, remove func(context.Context, string) error) error {
	for {
		idle, oldest, expired := 0, "", ""
		for id, usage := range m.projects {
			if usage.active == 0 {
				idle++
				if usage.completed >= maxPreparedUses {
					expired = id
				}
				if oldest == "" || usage.used < m.projects[oldest].used {
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
		delete(m.projects, oldest)
	}
}
