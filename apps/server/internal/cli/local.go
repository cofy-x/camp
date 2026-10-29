package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cofy-x/camp/apps/server/internal/server"
)

func runLocal(ctx context.Context, args []string, output io.Writer) error {
	name := args[0]
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var options serveOptions
	var printOnly, help bool
	flags.StringVar(&options.addr, "addr", "", "Server address")
	flags.StringVar(&options.dataDir, "data-dir", "", "Server data directory")
	flags.StringVar(&options.tlsCert, "tls-cert", "", "TLS certificate")
	flags.StringVar(&options.tlsKey, "tls-key", "", "TLS private key path")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&help, "h", false, "show help")
	if name == "open" {
		flags.BoolVar(&printOnly, "print", false, "print the address without opening a browser")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%s: %w; run 'camp %s --help'", name, err, name)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s does not accept positional arguments", name)
	}
	if help {
		_, err := fmt.Fprintf(output, "Usage: camp %s [--addr host:port] [--data-dir path] [--tls-cert path --tls-key path]\n", name)
		if name == "open" {
			_, err = fmt.Fprintln(output, "Use --print to print the address without launching a browser.")
		} else {
			_, err = fmt.Fprintln(output, "Checks local Server prerequisites only. No database, model request, configuration write or service is started.")
		}
		return err
	}
	var empty error
	flags.Visit(func(f *flag.Flag) {
		if f.Value.String() == "" {
			empty = fmt.Errorf("--%s requires a non-empty value", f.Name)
		}
	})
	if empty != nil {
		return empty
	}
	config, err := resolveConfig(options, os.Getenv, os.UserConfigDir)
	if err != nil {
		return err
	}
	address, err := browserURL(config)
	if err != nil {
		return err
	}
	if name == "open" {
		if _, err := fmt.Fprintln(output, address); err != nil {
			return err
		}
		if printOnly {
			return nil
		}
		return openBrowser(ctx, runtime.GOOS, address, func(ctx context.Context, command string, args ...string) error {
			return exec.CommandContext(ctx, command, args...).Run()
		})
	}
	directory, err := filepath.Abs(config.DataDir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(output, "Camp local Server\nAddress: %s\nTeam data: %s\n", address, directory)
	var failures []error
	if _, err := exec.LookPath("git"); err != nil {
		_, _ = fmt.Fprintln(output, "Git: missing; install Git before starting Server.")
		failures = append(failures, errors.New("Git is required for versioned files"))
	} else {
		_, _ = fmt.Fprintln(output, "Git: available")
	}
	info, err := os.Stat(directory)
	switch {
	case errors.Is(err, os.ErrNotExist):
		_, _ = fmt.Fprintln(output, "Storage: not created; Server will create it on first startup.")
	case err != nil:
		failures = append(failures, errors.New("cannot inspect the configured data directory"))
	case !info.IsDir() || info.Mode().Perm()&0077 != 0:
		failures = append(failures, errors.New("team data must be a private directory (mode 700)"))
	default:
		_, _ = fmt.Fprintln(output, "Storage: private directory present")
	}
	if runtime.GOOS == "darwin" {
		_, _ = fmt.Fprintln(output, "Mac Server: connect a Linux Runner for Agent work. PDF/DOCX preparation and local directory import require Linux.")
	}
	_, _ = fmt.Fprintln(output, "Next: start camp and complete Getting Ready in the browser. Model connectivity and Runner execution are verified there.")
	return errors.Join(failures...)
}

func browserURL(config server.Config) (string, error) {
	if config.PublicOrigin != "" {
		value, err := url.Parse(config.PublicOrigin)
		if err != nil || (value.Scheme != "http" && value.Scheme != "https") || value.Host == "" || value.User != nil || (value.Path != "" && value.Path != "/") || value.RawQuery != "" || value.Fragment != "" {
			return "", errors.New("CAMP_PUBLIC_ORIGIN must be an HTTP(S) origin without credentials, path, query or fragment")
		}
		return value.Scheme + "://" + value.Host, nil
	}
	host, port, err := net.SplitHostPort(config.Addr)
	if err != nil {
		return "", errors.New("cannot determine the local browser address")
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	if port == "0" {
		return "", errors.New("a dynamically allocated address is printed after Server starts; use that address in the browser")
	}
	scheme := "http"
	if config.TLSCert != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

func openBrowser(ctx context.Context, platform, address string, run func(context.Context, string, ...string) error) error {
	command := "xdg-open"
	if platform == "darwin" {
		command = "open"
	} else if platform != "linux" {
		return errors.New("open the printed address in your browser")
	}
	if err := run(ctx, command, address); err != nil {
		return errors.New("could not open a browser; open the printed address manually, or use 'camp open --print' on a headless host")
	}
	return nil
}
