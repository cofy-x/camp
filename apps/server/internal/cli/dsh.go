package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/runs"
)

var dshKeys = []string{
	"DEEPSEEK_API_KEY", "DEEPSEEK_BASE_URL", "CAMP_DSH_MODEL", "CAMP_DSH_RUNTIME",
	"CAMP_DSH_NODE", "CAMP_DSH_MAX_TOKENS", "CAMP_DSH_MAX_STEPS", "CAMP_DSH_TIMEOUT_SECONDS",
	"CAMP_LAYERFS_RUNTIME", "CAMP_SANDBOX_HELPER",
}

// readDSHConfig reads only the selected private file and allowed variables without changing the environment or evaluating shell syntax.
func readDSHConfig(options serveOptions, getenv func(string) string, cwd string) (runs.Config, error) {
	filename := options.envFile
	if filename == "" {
		filename = getenv("CAMP_ENV_FILE")
	}
	explicitFile := filename != ""
	if filename == "" {
		filename = ".env"
	}
	if !filepath.IsAbs(filename) {
		filename = filepath.Join(cwd, filename)
	}
	values, err := readPrivateEnv(filename, explicitFile)
	if err != nil {
		return runs.Config{}, err
	}

	overrides := &configuration.Overrides{Environment: map[string]string{}, EnvFile: map[string]string{}}
	modelFields := map[string]string{"DEEPSEEK_API_KEY": "apiKey", "DEEPSEEK_BASE_URL": "baseUrl", "CAMP_DSH_MODEL": "model", "CAMP_DSH_MAX_TOKENS": "maxTokens", "CAMP_DSH_MAX_STEPS": "maxSteps", "CAMP_DSH_TIMEOUT_SECONDS": "timeoutSeconds"}
	for key, field := range modelFields {
		if values[key] != "" {
			overrides.EnvFile[field] = values[key]
		}
	}
	for _, key := range dshKeys {
		if value := getenv(key); value != "" {
			values[key] = value
			if field := modelFields[key]; field != "" {
				overrides.Environment[field] = value
			}
		}
		if !utf8.ValidString(values[key]) || strings.ContainsAny(values[key], "\x00\r\n") {
			return runs.Config{}, fmt.Errorf("%s must be a single-line UTF-8 value", key)
		}
	}
	value := func(key, fallback string) string {
		if values[key] != "" {
			return values[key]
		}
		return fallback
	}
	node, _ := exec.LookPath("node")
	config := runs.Config{
		ModelOverrides:    overrides,
		APIKey:            values["DEEPSEEK_API_KEY"],
		BaseURL:           value("DEEPSEEK_BASE_URL", "https://api.deepseek.com/anthropic"),
		Model:             value("CAMP_DSH_MODEL", "deepseek-v4-pro"),
		RuntimeDir:        value("CAMP_DSH_RUNTIME", filepath.Join(cwd, "tools", "dsh-runtime")),
		NodePath:          value("CAMP_DSH_NODE", node),
		LayerFSRuntimeDir: value("CAMP_LAYERFS_RUNTIME", filepath.Join(cwd, "tools", "layerfs-runtime")),
		SandboxHelper:     value("CAMP_SANDBOX_HELPER", filepath.Join(cwd, "dist", "camp-sandbox")),
		DockerSocket:      getenv("CAMP_DOCKER_SOCKET"),
	}
	if !filepath.IsAbs(config.RuntimeDir) {
		config.RuntimeDir = filepath.Join(cwd, config.RuntimeDir)
	}
	for _, target := range []*string{&config.LayerFSRuntimeDir, &config.SandboxHelper} {
		if !filepath.IsAbs(*target) {
			*target = filepath.Join(cwd, *target)
		}
	}
	parsedURL, err := url.Parse(config.BaseURL)
	if err != nil || parsedURL.Hostname() == "" || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return runs.Config{}, errors.New("DEEPSEEK_BASE_URL must be an HTTP(S) URL without credentials, query parameters, or a fragment")
	}
	config.MaxTokens, err = dshBudget(values, "CAMP_DSH_MAX_TOKENS", 4096, 8192)
	if err != nil {
		return runs.Config{}, err
	}
	config.MaxSteps, err = dshBudget(values, "CAMP_DSH_MAX_STEPS", 8, 32)
	if err != nil {
		return runs.Config{}, err
	}
	seconds, err := dshBudget(values, "CAMP_DSH_TIMEOUT_SECONDS", 180, 900)
	if err != nil {
		return runs.Config{}, err
	}
	config.Timeout = time.Duration(seconds) * time.Second
	return config, nil
}

func dshBudget(values map[string]string, key string, fallback, maximum int) (int, error) {
	if values[key] == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(values[key])
	if err != nil || n < 1 || n > maximum {
		return 0, fmt.Errorf("%s must be an integer between 1 and %d", key, maximum)
	}
	return n, nil
}

func readPrivateEnv(filename string, required bool) (map[string]string, error) {
	info, err := os.Stat(filename)
	if !required && errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read private DSH configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("private DSH configuration must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private DSH configuration must allow access to its owner only (chmod 600)")
	}
	const maxEnvFile = 1 << 20
	if info.Size() > maxEnvFile {
		return nil, errors.New("private DSH configuration exceeds 1 MiB")
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open private DSH configuration: %w", err)
	}
	defer file.Close()
	return parseDSHEnv(io.LimitReader(file, maxEnvFile+1))
}

func parseDSHEnv(input io.Reader) (map[string]string, error) {
	allowed := make(map[string]bool, len(dshKeys))
	for _, key := range dshKeys {
		allowed[key] = true
	}
	values := make(map[string]string, len(dshKeys))
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if lineNumber == 1 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		if strings.HasPrefix(line, "export ") || strings.HasPrefix(line, "export\t") {
			line = strings.TrimSpace(line[len("export"):])
		}
		end := 0
		for end < len(line) && ((line[end] >= 'A' && line[end] <= 'Z') || (line[end] >= 'a' && line[end] <= 'z') || line[end] == '_' || (line[end] >= '0' && line[end] <= '9')) {
			end++
		}
		key := line[:end]
		if !allowed[key] {
			continue
		}
		remaining := strings.TrimSpace(line[end:])
		if !strings.HasPrefix(remaining, "=") {
			return nil, fmt.Errorf("DSH configuration line %d: %s requires KEY=value syntax", lineNumber, key)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("DSH configuration line %d: %s is defined more than once", lineNumber, key)
		}
		value, valid := literalEnvValue(strings.TrimSpace(remaining[1:]))
		if !valid || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("DSH configuration line %d: %s has an invalid single-line value", lineNumber, key)
		}
		values[key] = value
	}
	if scanner.Err() != nil {
		return nil, fmt.Errorf("cannot read DSH configuration near line %d: a line exceeds 64 KiB or the file could not be read", lineNumber+1)
	}
	return values, nil
}

// Values are literal, with single-line quotes and comments after whitespace; escapes, interpolation, and commands are not evaluated.
func literalEnvValue(raw string) (string, bool) {
	if raw == "" {
		return "", true
	}
	if raw[0] == '\'' || raw[0] == '"' {
		end := strings.IndexByte(raw[1:], raw[0])
		if end < 0 {
			return "", false
		}
		end++
		tail := strings.TrimSpace(raw[end+1:])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return "", false
		}
		return raw[1:end], true
	}
	if raw[0] == '#' {
		return "", true
	}
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			return strings.TrimSpace(raw[:i]), true
		}
	}
	return raw, true
}
