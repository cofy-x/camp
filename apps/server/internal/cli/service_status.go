package cli

import "bytes"

// Service facts are read-only observations. Installed/login-enabled do not
// imply a loaded or running process, and unknown never authorizes replacement.
type serviceStatus struct {
	Reason       string `json:"reason,omitempty"`
	Supported    bool   `json:"supported"`
	Installed    bool   `json:"installed"`
	Loaded       bool   `json:"loaded"`
	Running      bool   `json:"running"`
	LoginEnabled bool   `json:"loginEnabled"`
	State        string `json:"state"`
}

// A print probe never buffers unbounded launchd diagnostics.
type serviceObservationOutput struct{ bytes.Buffer }

func (b *serviceObservationOutput) Write(data []byte) (int, error) {
	n := len(data)
	if left := (64 << 10) - b.Len(); left > 0 {
		_, _ = b.Buffer.Write(data[:min(left, len(data))])
	}
	return n, nil
}
