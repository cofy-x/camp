//go:build !darwin

package server

import (
	"context"
	"log/slog"
)

// Linux reader processes used a parent-bound private PID namespace and have no
// persisted Mac launchd jobs to reconcile. Historical files remain untouched.
func recoverLegacyDocumentJobs(context.Context, string, *slog.Logger) {}
