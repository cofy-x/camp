package cli

import (
	"context"
	"errors"
	"sync"
)

const runtimeIOConcurrency = 8

// runtimeIO bounds open files and drains admitted work before staging cleanup.
// Cancellation stops admission, never the obligation to close/sync an owned file.
type runtimeIO struct {
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	err    error
}

func newRuntimeIO(ctx context.Context, concurrency int) *runtimeIO {
	ctx, cancel := context.WithCancel(ctx)
	return &runtimeIO{ctx: ctx, cancel: cancel, slots: make(chan struct{}, concurrency)}
}
func (g *runtimeIO) reserve() error {
	if g.ctx.Err() != nil {
		return g.result()
	}
	select {
	case g.slots <- struct{}{}:
		if g.ctx.Err() != nil {
			<-g.slots
			return g.result()
		}
		return nil
	case <-g.ctx.Done():
		return g.result()
	}
}
func (g *runtimeIO) release() { <-g.slots }
func (g *runtimeIO) start(work func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer g.release()
		if err := work(); err != nil {
			g.mu.Lock()
			if g.err == nil {
				g.err = err
			}
			g.mu.Unlock()
			g.cancel()
		}
	}()
}
func (g *runtimeIO) run(work func() error) error {
	if err := g.reserve(); err != nil {
		return err
	}
	g.start(work)
	return nil
}
func (g *runtimeIO) result() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.err != nil {
		return g.err
	}
	return g.ctx.Err()
}
func (g *runtimeIO) wait() error { g.wg.Wait(); return g.result() }
func (g *runtimeIO) finish(err *error) {
	if *err != nil {
		g.cancel()
	}
	*err = errors.Join(*err, g.wait())
	g.cancel()
}
