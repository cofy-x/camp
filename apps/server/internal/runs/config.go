// Package runs owns durable agent execution, admission, cancellation, and result publication.
package runs

import (
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
)

type Config struct {
	RuntimeDir         string
	DocumentRuntimeDir string
	LayerFSRuntimeDir  string
	SandboxHelper      string
	DockerSocket       string
	NodePath           string
	APIKey             string
	BaseURL            string
	Model              string
	MaxTokens          int
	MaxSteps           int
	Timeout            time.Duration
	ModelOverrides     *configuration.Overrides
}

type Budget struct {
	MaxTokens      int `json:"maxTokens"`
	MaxSteps       int `json:"maxSteps"`
	TimeoutSeconds int `json:"timeoutSeconds"`
}

func (c Config) modelDefaults() configuration.Model {
	model := configuration.Defaults()
	if c.ModelOverrides != nil {
		return model
	}
	model.APIKey = c.APIKey
	if c.Model != "" {
		model.Model = c.Model
	}
	if c.BaseURL != "" {
		model.BaseURL = c.BaseURL
	}
	if c.MaxTokens > 0 {
		model.MaxTokens = c.MaxTokens
	}
	if c.MaxSteps > 0 {
		model.MaxSteps = c.MaxSteps
	}
	if c.Timeout > 0 {
		model.TimeoutSeconds = int(c.Timeout / time.Second)
	}
	return model
}
