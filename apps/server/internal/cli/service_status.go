package cli

// Service facts are read-only observations. Installed/login-enabled do not
// imply a loaded or running process, and unknown never authorizes replacement.
type serviceStatus struct {
	Supported    bool   `json:"supported"`
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	Running      bool   `json:"running"`
	LoginEnabled bool   `json:"loginEnabled"`
	State        string `json:"state"`
}
