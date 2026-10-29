package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cofy-x/camp/apps/server/internal/cli"
)

// Override with go build -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cli.Run(ctx, os.Args[1:], os.Stdout, version); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(cli.ExitCode(err))
	}
}
