package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/server"
)

const appName = "camp"

type commandKind int

const (
	commandServe commandKind = iota
	commandHelp
	commandServeHelp
	commandVersion
)

type command struct {
	kind    commandKind
	options serveOptions
}

// Run dispatches a command, resolving service configuration only when needed.
func Run(ctx context.Context, args []string, output io.Writer, version string) error {
	if len(args) > 0 && (args[0] == "doctor" || args[0] == "open") {
		return runLocal(ctx, args, output)
	}
	if len(args) > 0 && args[0] == "runner" {
		return runRunner(ctx, args[1:], output)
	}
	// The isolated parser must never load service configuration or credentials.
	if len(args) == 1 && args[0] == "document-worker" {
		return documenttext.Worker(ctx, os.Stdin, output)
	}
	cmd, err := parseCommand(args)
	if err != nil {
		return err
	}
	switch cmd.kind {
	case commandHelp:
		_, err = fmt.Fprintf(output, `Usage: %[1]s [command]

Start the local desktop service when no command is given.

Commands:
  serve     Start the service (use "serve --help" for options)
  doctor    Check local Server prerequisites without changing data
  open      Open the browser address (use --print on headless hosts)
  runner    Install, connect, run, or inspect a Camp Runner
  help      Show this help (--help, -h)
  version   Print the version (--version)

Examples:
  %[1]s
  %[1]s serve --addr 127.0.0.1:3002 --data-dir ./data
`, appName)
		return err
	case commandServeHelp:
		_, err = fmt.Fprintf(output, `Usage: %s serve [options]

Options:
  --addr host:port   HTTP listen address
  --data-dir path    Directory containing desktop.sqlite
  --allowed-hosts hosts     Additional HTTP hostnames/IPs (comma-separated, no ports)
  --trusted-origins origins Trusted browser origins (comma-separated, exact)
  --env-file path           Private server-side DSH configuration file
  --tls-cert path           TLS certificate chain (requires --tls-key)
  --tls-key path            Private TLS key (requires --tls-cert)
  --max-concurrent-runs n   Server execution limit (default 2)
  --allow-fixture-runners   Accept clearly labeled test executors (development only)
  --help, -h                Show this help

Configuration precedence:
  Address: --addr > ADDR > 127.0.0.1:PORT > 127.0.0.1:3001
  Data:    --data-dir > DATA_DIR > OS user config directory/%s
  Hosts:   --allowed-hosts > ALLOWED_HOSTS
  Origins: --trusted-origins > TRUSTED_ORIGINS
  Env file: --env-file > CAMP_ENV_FILE > ./.env (optional default)
  DSH: non-empty process environment > selected env file > managed Settings > defaults

Empty environment variables are treated as unset. Explicit flag values must
not be empty. Put options after "serve"; positional arguments are not accepted.
`, appName, appName)
		return err
	case commandVersion:
		_, err = fmt.Fprintf(output, "%s %s\n", appName, version)
		return err
	default:
		config, err := resolveConfig(cmd.options, os.Getenv, os.UserConfigDir)
		if err != nil {
			return err
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
		config.DocumentRuntimeDir = documentRuntime(os.Getenv, cwd)
		config.DSH, err = readDSHConfig(cmd.options, os.Getenv, cwd)
		if err != nil {
			return err
		}
		config.OnReady = func(address, setupPath string) {
			config.Addr = address
			if url, err := browserURL(config); err == nil {
				_, _ = fmt.Fprintf(output, "Open Camp: %s\n", url)
			}
			directory, _ := filepath.Abs(config.DataDir)
			_, _ = fmt.Fprintf(output, "Team data: %s\n", directory)
			if setupPath != "" {
				_, _ = fmt.Fprintf(output, "Read the private setup token from %s, create your team, then follow Getting Ready.\n", setupPath)
			}
		}
		return server.Run(ctx, config)
	}
}

func documentRuntime(getenv func(string) string, cwd string) string {
	runtime := getenv("CAMP_DOCUMENT_RUNTIME")
	if runtime == "" {
		return filepath.Join(cwd, "tools", "document-runtime")
	}
	if !filepath.IsAbs(runtime) {
		return filepath.Join(cwd, runtime)
	}
	return runtime
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{kind: commandServe}, nil
	}
	var kind commandKind
	switch args[0] {
	case "serve":
		return parseServe(args[1:])
	case "help", "--help", "-h":
		kind = commandHelp
	case "version", "--version":
		kind = commandVersion
	default:
		return command{}, fmt.Errorf("unknown command %q; run '%s help' for usage", args[0], appName)
	}
	if len(args) != 1 {
		return command{}, fmt.Errorf("%s does not accept arguments: %q", args[0], args[1:])
	}
	return command{kind: kind}, nil
}

func parseServe(args []string) (command, error) {
	cmd := command{kind: commandServe}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var help bool
	flags.BoolVar(&help, "help", false, "show serve help")
	flags.BoolVar(&help, "h", false, "show serve help")
	flags.StringVar(&cmd.options.addr, "addr", "", "HTTP listen address")
	flags.StringVar(&cmd.options.dataDir, "data-dir", "", "database directory")
	flags.StringVar(&cmd.options.allowedHosts, "allowed-hosts", "", "additional HTTP hostnames or IPs, comma-separated without ports")
	flags.StringVar(&cmd.options.trustedOrigins, "trusted-origins", "", "trusted browser origins, comma-separated")
	flags.StringVar(&cmd.options.envFile, "env-file", "", "private server-side DSH configuration file")
	flags.StringVar(&cmd.options.tlsCert, "tls-cert", "", "TLS certificate chain")
	flags.StringVar(&cmd.options.tlsKey, "tls-key", "", "private TLS key")
	flags.BoolVar(&cmd.options.allowFixture, "allow-fixture-runners", false, "allow test executors")
	flags.IntVar(&cmd.options.maxConcurrent, "max-concurrent-runs", 0, "maximum concurrent executions")
	if err := flags.Parse(args); err != nil {
		return command{}, fmt.Errorf("serve: %w; run '%s serve --help' for usage", err, appName)
	}
	if flags.NArg() != 0 {
		return command{}, fmt.Errorf("serve: unexpected arguments %q", flags.Args())
	}
	if help {
		return command{kind: commandServeHelp}, nil
	}
	var valueErr error
	flags.Visit(func(f *flag.Flag) {
		if f.Name != "addr" && f.Name != "data-dir" && f.Name != "allowed-hosts" && f.Name != "trusted-origins" && f.Name != "env-file" && f.Name != "tls-cert" && f.Name != "tls-key" {
			return
		}
		value := f.Value.String()
		if strings.TrimSpace(value) == "" {
			valueErr = fmt.Errorf("serve: --%s requires a non-empty value", f.Name)
		} else if strings.HasPrefix(value, "-") {
			valueErr = fmt.Errorf("serve: --%s needs a value before %q (prefix paths starting with '-' with './')", f.Name, value)
		}
	})
	return cmd, valueErr
}
