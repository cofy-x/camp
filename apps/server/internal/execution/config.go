package execution

// Config contains only the inputs needed to construct an isolated process.
type Config struct {
	RuntimeDir, NodePath, APIKey, BaseURL string
	StateDir, ReferencesDir               string
	// CAFile is a private, explicit trust bundle for the Camp model proxy.
	// It is exposed only to DSH, never to model tools or the workspace.
	CAFile          string
	Acceptance      bool
	ReadOnlyProject bool
	Dependencies    []Mount
	PnpmPath        string
	// RemoteTools gives DSH only a virtual cwd; model files and commands go to
	// this Run's authenticated LayerFS tool service instead of a host copy.
	RemoteTools                             bool
	ToolEndpoint, ToolToken                 string
	CoordinationEndpoint, CoordinationToken string
}

// Mount exposes a prepared dependency directory at a relative project path.
type Mount struct {
	Source string
	Target string
}

// ProjectConfig runs a bounded project operation without model credentials.
type ProjectConfig struct {
	NodePath, PnpmPath, ProjectDir, Directory, CacheDir string
	Arguments                                           []string
	Dependencies                                        []Mount
	Network, ReadOnly                                   bool
}
