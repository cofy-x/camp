package configuration

type ReadinessComponent struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Diagnostic
}
type Readiness struct {
	Ready      bool                 `json:"ready"`
	CheckedAt  string               `json:"checkedAt"`
	Components []ReadinessComponent `json:"components"`
}
type EnvironmentSettings struct {
	Platform       string         `json:"platform"`
	NodeVersion    string         `json:"nodeVersion"`
	PackageManager string         `json:"packageManager"`
	CommandNetwork bool           `json:"commandNetwork"`
	Concurrency    int            `json:"concurrency"`
	Checks         string         `json:"checks"`
	Limits         map[string]int `json:"limits"`
}
type StorageSettings struct {
	DataDirectory string `json:"dataDirectory"`
	Files         string `json:"files"`
	Versions      string `json:"versions"`
	Backups       string `json:"backups"`
}

type Budget struct {
	MaxTokens      int `json:"maxTokens"`
	MaxSteps       int `json:"maxSteps"`
	TimeoutSeconds int `json:"timeoutSeconds"`
}
