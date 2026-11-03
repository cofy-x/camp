package documenttext

import (
	"context"
	"errors"
	"time"
)

var errReaderOwnership = errors.New("Document reader descendant cleanup is unconfirmed")

// Check is a cached observation of a model-free synthetic reader probe.
type Check struct {
	Format    string
	Status    string
	Detail    string
	CheckedAt string
}

// CheckReaders runs at Server startup. Reading readiness never starts workers.
func (e *Engine) CheckReaders(ctx context.Context) {
	for _, format := range []string{"docx", "pdf"} {
		check := Check{Format: format, Status: "ready", Detail: "Isolated document reader verified with a synthetic document.", CheckedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := e.Probe(ctx, format); err != nil {
			check.Status, check.Detail = "unavailable", "The isolated document reader is unavailable. Originals remain downloadable."
			var problem *Failure
			if errors.As(err, &problem) {
				check.Detail = problem.Message
			}
		}
		e.publishCheck(check)
	}
}

func (e *Engine) publishCheck(check Check) {
	// A probe result may become stale after Extract releases execution ownership.
	// Publish under the same runMu -> stateMu order as suspension, so a successful
	// earlier probe cannot replace a later unknown-owner observation.
	e.runMu.Lock()
	defer e.runMu.Unlock()
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	if e.suspended != nil && check.Status == "ready" {
		check.Status, check.Detail = "unavailable", "Document reader ownership or installation needs attention; originals remain downloadable."
	}
	for i := range e.checks {
		if e.checks[i].Format == check.Format {
			e.checks[i] = check
		}
	}
}

func (e *Engine) markUnavailable() {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	for i := range e.checks {
		e.checks[i].Status, e.checks[i].Detail = "unavailable", "Document reader ownership or installation needs attention; originals remain downloadable."
	}
}

func (e *Engine) Readiness() []Check {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return append([]Check(nil), e.checks...)
}
