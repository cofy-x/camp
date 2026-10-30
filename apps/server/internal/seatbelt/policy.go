// Package seatbelt turns explicit native execution grants into a default-deny
// macOS profile. It does not own processes, workspaces or result publication.
package seatbelt

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Policy struct {
	// List grants only the directory's own entries, without reading children.
	List       []string
	Read       []string
	Write      []string
	Executable []string
	// Ports are exact private DSH control endpoints, never command network
	// authority. An empty list denies IP, UDP, Unix sockets and local binding.
	Ports []uint16
}

var systemRead = []string{"/System/Library", "/usr/lib", "/usr/share", "/usr/bin", "/bin", "/usr/sbin", "/sbin", "/dev/null", "/dev/zero", "/dev/random", "/dev/urandom"}
var systemExec = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

func endpointPort(endpoint string) (uint16, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() != "127.0.0.1" {
		return 0, errors.New("Native control endpoints require an exact loopback HTTP origin")
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return 0, errors.New("Native control endpoints require an explicit port")
	}
	return uint16(port), nil
}

func ControlPorts(endpoints ...string) ([]uint16, error) {
	var ports []uint16
	for _, endpoint := range endpoints {
		if endpoint == "" {
			continue
		}
		port, err := endpointPort(endpoint)
		if err != nil {
			return nil, err
		}
		ports = append(ports, port)
	}
	return ports, nil
}

// ValidateRoots checks explicit filesystem grants without compiling a profile.
func ValidateRoots(paths []string) error {
	_, err := literalPaths(paths)
	return err
}

func literalPaths(paths []string) ([]string, error) {
	seen := map[string]bool{}
	for _, name := range paths {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\x00\r\n") {
			return nil, errors.New("Sandbox grants require normalized absolute literal paths")
		}
		resolved, err := filepath.EvalSymlinks(name)
		if err != nil {
			return nil, err
		}
		for _, spelling := range []string{name, resolved} {
			home, homeErr := os.UserHomeDir()
			if homeErr == nil && (spelling == home || strings.HasPrefix(home, spelling+string(filepath.Separator))) {
				return nil, errors.New("Sandbox grants cannot expose the user's entire home")
			}
			switch spelling {
			case "/", "/System", "/System/Volumes", "/System/Volumes/Data", "/Users", "/Volumes", "/private", "/private/var", "/var", "/tmp", "/private/tmp", "/opt", "/opt/homebrew", "/usr", "/usr/local", "/Library":
				return nil, fmt.Errorf("Sandbox grant is too broad: %s", spelling)
			}
			seen[spelling] = true
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

func Profile(policy Policy) (string, error) {
	list, err := literalPaths(policy.List)
	if err != nil {
		return "", err
	}
	read, err := literalPaths(append(append([]string{}, systemRead...), policy.Read...))
	if err != nil {
		return "", err
	}
	write, err := literalPaths(policy.Write)
	if err != nil {
		return "", err
	}
	executable, err := literalPaths(append(append([]string{}, systemExec...), policy.Executable...))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n(allow process-fork)\n(allow process-info* (target same-sandbox))\n(allow signal (target same-sandbox))\n(allow sysctl-read)\n")
	// Traversal metadata is not directory listing/file data. Root's own dirent
	// names are needed by dyld; no Data-volume or home subtree is reallowed.
	b.WriteString("(allow file-read-metadata (vnode-type DIRECTORY))\n(allow file-read* (literal \"/\"))\n")
	b.WriteString("(allow mach-lookup (global-name \"com.apple.system.logger\") (global-name \"com.apple.logd\"))\n")
	add := func(operation string, roots []string) {
		for _, root := range roots {
			filter := "literal"
			if info, err := os.Stat(root); err == nil && info.IsDir() {
				filter = "subpath"
			}
			fmt.Fprintf(&b, "(allow %s (%s %s))\n", operation, filter, strconv.Quote(root))
		}
	}
	add("file-read*", read)
	for _, name := range list {
		info, err := os.Stat(name)
		if err != nil || !info.IsDir() {
			return "", errors.New("Directory listing grants require a directory")
		}
		fmt.Fprintf(&b, "(allow file-read-data (literal %s))\n", strconv.Quote(name))
	}
	add("file-read* file-write*", write)
	add("process-exec", executable)
	// A path's ancestors can include OS aliases such as /var. Directory-only
	// metadata does not cover the symlink itself, so grant only each literal
	// ancestor needed to reach an approved root. This permits no file data or
	// directory listing in those ancestors.
	ancestors := map[string]bool{}
	for _, roots := range [][]string{read, write, executable} {
		for _, root := range roots {
			for parent := filepath.Dir(root); parent != "/"; parent = filepath.Dir(parent) {
				ancestors[parent] = true
			}
		}
	}
	var metadata []string
	for name := range ancestors {
		metadata = append(metadata, name)
	}
	sort.Strings(metadata)
	for _, name := range metadata {
		fmt.Fprintf(&b, "(allow file-read-metadata (literal %s))\n", strconv.Quote(name))
	}
	// Executable mappings are a separate Seatbelt operation. Grant them only
	// for readable runtime/tool roots, never for unrelated host file data.
	add("file-map-executable", read)
	add("file-map-executable", executable)
	b.WriteString("(allow file-write-data file-ioctl (literal \"/dev/null\"))\n")
	for _, port := range policy.Ports {
		if port == 0 {
			return "", errors.New("Sandbox control ports must be nonzero")
		}
		fmt.Fprintf(&b, "(allow network-outbound (require-all (remote tcp \"localhost:%d\") (socket-domain AF_INET)))\n", port)
	}
	return b.String(), nil
}
