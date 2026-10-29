package documenttext

import (
	"context"
	"testing"
)

func TestIntegrationReadinessProbes(t *testing.T) {
	engine := integrationEngine(t)
	for _, format := range []string{"pdf", "docx"} {
		t.Run(format, func(t *testing.T) {
			if err := engine.Probe(context.Background(), format); err != nil {
				t.Fatal(err)
			}
		})
	}
}
