// Camp's Go-installable entrypoint runs the matching complete release package.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"github.com/cofy-x/camp/internal/launcher"
)

var version = ""

func installedVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && launcher.ValidVersion(info.Main.Version) {
		return info.Main.Version
	}
	return "dev"
}

func main() {
	v := installedVersion()
	args := os.Args[1:]
	if len(args) == 1 {
		switch args[0] {
		case "version", "--version":
			fmt.Printf("camp %s\n", v)
			return
		case "help", "--help", "-h":
			fmt.Println(`Usage: camp [command]

Commands:
  serve     Start Camp Server (default; use "serve --help" for options)
  runner    Set up or operate a supported Linux Runner
  version   Print this fixed installation's version

The first invocation downloads and verifies this version's full release package.
Subsequent invocations use its private cache. No service starts automatically.
Go installation does not require Node, pnpm, or a source checkout.`)
			return
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	role := "server"
	if len(args) > 0 && args[0] == "runner" {
		role = "runner"
	}
	cache, err := os.UserCacheDir()
	if err == nil {
		var binary string
		binary, err = launcher.Ensure(ctx, launcher.Options{Version: v, Role: role, OS: runtime.GOOS, Arch: runtime.GOARCH, CacheDir: cache, Output: os.Stderr})
		if err == nil {
			stop()
			err = execute(binary, args)
		}
	}
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
