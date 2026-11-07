package configuration

import "time"

// ModelInput contains only model defaults and explicit private overrides.
// Native runtimes and document workers have separate configuration owners.
type ModelInput struct {
	APIKey, BaseURL, Model, ReasoningEffort string
	MaxTokens, MaxSteps                     int
	Timeout                                 time.Duration
	ModelOverrides                          *Overrides
}

func (c ModelInput) ModelDefaults() Model {
	model := Defaults()
	if c.ModelOverrides != nil {
		return model
	}
	model.APIKey = c.APIKey
	model.ReasoningEffort = c.ReasoningEffort
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
