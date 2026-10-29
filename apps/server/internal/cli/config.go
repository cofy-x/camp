package cli

import (
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/server"
)

type serveOptions struct {
	tlsCert        string
	tlsKey         string
	allowFixture   bool
	maxConcurrent  int
	addr           string
	dataDir        string
	allowedHosts   string
	trustedOrigins string
	envFile        string
}

func resolveConfig(options serveOptions, getenv func(string) string, userConfigDir func() (string, error)) (server.Config, error) {
	config := server.Config{Addr: options.addr, DataDir: options.dataDir, PublicOrigin: getenv("CAMP_PUBLIC_ORIGIN")}
	config.TLSCert, config.TLSKey = options.tlsCert, options.tlsKey
	if config.TLSCert == "" {
		config.TLSCert = getenv("CAMP_TLS_CERT")
	}
	if config.TLSKey == "" {
		config.TLSKey = getenv("CAMP_TLS_KEY")
	}
	if (config.TLSCert == "") != (config.TLSKey == "") {
		return server.Config{}, fmt.Errorf("configure both CAMP_TLS_CERT and CAMP_TLS_KEY, or both TLS flags")
	}
	config.AllowFixtureRunners = options.allowFixture
	config.MaxConcurrentRuns = options.maxConcurrent
	if config.MaxConcurrentRuns == 0 {
		if value := getenv("CAMP_MAX_CONCURRENT_RUNS"); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 64 {
				return server.Config{}, fmt.Errorf("CAMP_MAX_CONCURRENT_RUNS must be between 1 and 64")
			}
			config.MaxConcurrentRuns = n
		}
	}
	if config.MaxConcurrentRuns < 0 || config.MaxConcurrentRuns > 64 {
		return server.Config{}, fmt.Errorf("max concurrent runs must be between 1 and 64")
	}
	if config.Addr == "" {
		config.Addr = getenv("ADDR")
	}
	if config.Addr == "" {
		port := getenv("PORT")
		if port == "" {
			port = "3001"
		}
		config.Addr = net.JoinHostPort("127.0.0.1", port)
	}
	_, port, err := net.SplitHostPort(config.Addr)
	if err != nil {
		return server.Config{}, fmt.Errorf("invalid listen address %q: %w", config.Addr, err)
	}
	if port == "" {
		return server.Config{}, fmt.Errorf("invalid listen address %q: port is required", config.Addr)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return server.Config{}, fmt.Errorf("invalid listen address %q: %w", config.Addr, err)
	}
	if config.DataDir == "" {
		config.DataDir = getenv("DATA_DIR")
	}
	if config.DataDir == "" {
		base, err := userConfigDir()
		if err != nil {
			return server.Config{}, fmt.Errorf("resolve data directory: %w", err)
		}
		config.DataDir = filepath.Join(base, appName)
	}
	for _, value := range []struct {
		option, env string
		target      *[]string
	}{
		{options.allowedHosts, "ALLOWED_HOSTS", &config.AllowedHosts},
		{options.trustedOrigins, "TRUSTED_ORIGINS", &config.TrustedOrigins},
		{"", "CAMP_IMPORT_ROOTS", &config.ImportRoots},
	} {
		raw := value.option
		if raw == "" {
			raw = getenv(value.env)
		}
		if raw == "" {
			continue
		}
		for item := range strings.SplitSeq(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				return server.Config{}, fmt.Errorf("%s cannot contain an empty item", value.env)
			}
			*value.target = append(*value.target, item)
		}
	}
	return config, nil
}
