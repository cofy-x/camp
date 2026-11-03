// Package runs provides the local execution regression harness and shared
// execution configuration. Production execution is owned by remoteruns.
package runs

import (
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
)

type Config struct {
	RuntimeDir        string
	LayerFSRuntimeDir string
	SandboxHelper     string
	DockerSocket      string
	NodePath          string
	APIKey            string
	BaseURL           string
	Model             string
	MaxTokens         int
	MaxSteps          int
	Timeout           time.Duration
	ModelOverrides    *configuration.Overrides
}

type Budget = configuration.Budget

func (c Config) modelDefaults() configuration.Model {
	return (configuration.ModelInput{APIKey: c.APIKey, BaseURL: c.BaseURL, Model: c.Model, MaxTokens: c.MaxTokens, MaxSteps: c.MaxSteps, Timeout: c.Timeout, ModelOverrides: c.ModelOverrides}).ModelDefaults()
}
