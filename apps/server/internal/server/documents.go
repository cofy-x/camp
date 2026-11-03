package server

import (
	"context"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
)

// Server reader observations have their own owner; they never affect execution
// admission and never start a process when the settings endpoint is read.
type serverSettings struct {
	*remoteruns.Manager
	documents *documenttext.Engine
}

func (s serverSettings) Readiness(ctx context.Context) (configuration.Readiness, error) {
	value, err := s.Manager.Readiness(ctx)
	if err != nil {
		return value, err
	}
	for _, check := range s.documents.Readiness() {
		value.Components = append(value.Components, documentDiagnostic(check))
	}
	return value, nil
}

func documentDiagnostic(check documenttext.Check) configuration.ReadinessComponent {
	// Parser lifecycle states and Settings diagnostics have separate vocabularies.
	// Keep reader availability inside the existing public diagnostic contract.
	status := "error"
	switch check.Status {
	case "ready", "unknown":
		status = check.Status
	case "unavailable":
		status = "missing"
	}
	return configuration.ReadinessComponent{ID: "document-" + check.Format, Label: strings.ToUpper(check.Format) + " text preparation", Diagnostic: configuration.Diagnostic{Status: status, Detail: check.Detail, CheckedAt: check.CheckedAt}}
}
