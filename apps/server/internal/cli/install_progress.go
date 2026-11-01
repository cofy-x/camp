package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// One phase owns its ticker and output until work settles. Counters describe
// processed bytes/entries; no phase invents an ETA or treats transfer as setup.
type installPhase struct {
	mu             sync.Mutex
	bytes, entries int64
	total          int64
	started        time.Time
	label          string
	output         io.Writer
}

func (p *installPhase) advance(bytes, entries int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytes += bytes
	p.entries += entries
}
func (p *installPhase) transferred(bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bytes = bytes
}
func installationSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1<<20 {
		return fmt.Sprintf("%.1f KiB", float64(bytes)/(1<<10))
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/(1<<20))
}
func (p *installPhase) report(state string) {
	p.mu.Lock()
	bytes, entries := p.bytes, p.entries
	p.mu.Unlock()
	fmt.Fprintf(p.output, "%s: %s", p.label, state)
	if p.total > 0 {
		fmt.Fprintf(p.output, "; %s / %s", installationSize(bytes), installationSize(p.total))
	} else if bytes > 0 {
		fmt.Fprintf(p.output, "; %s processed", installationSize(bytes))
	}
	if entries > 0 {
		fmt.Fprintf(p.output, "; %d entries processed", entries)
	}
	fmt.Fprintf(p.output, "; elapsed %s\n", time.Since(p.started).Round(time.Millisecond))
}
func installationPhase(ctx context.Context, output io.Writer, label string, total int64, work func(*installPhase) error) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	return installationPhaseTicks(ctx, output, label, total, ticker.C, work)
}
func installationPhaseTicks(ctx context.Context, output io.Writer, label string, total int64, ticks <-chan time.Time, work func(*installPhase) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	phase := &installPhase{total: total, started: time.Now(), label: label, output: output}
	fmt.Fprintln(output, label+"…")
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ticks:
				state := "working"
				if ctx.Err() != nil {
					state = "cancelling"
				}
				phase.report(state)
			}
		}
	}()
	err := func() error { defer func() { close(stop); <-done }(); return work(phase) }()
	state := "complete"
	if err != nil {
		state = "failed"
		if ctx.Err() != nil {
			state = "cancelled"
		}
	}
	phase.report(state)
	return err
}

type installReader struct {
	ctx   context.Context
	input io.Reader
	phase *installPhase
}

func (r installReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(data)
	r.phase.advance(int64(n), 0)
	return n, err
}
