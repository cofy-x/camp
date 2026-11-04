package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/cofy-x/camp/apps/server/internal/fsdurability"
	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) == 3 && os.Args[1] == "sync-staging-tree" {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		// The trusted parent keeps this pipe open. Parent death or unexpected
		// input cancels admission; this helper never spawns another process.
		go func() { _, _ = io.Copy(io.Discard, io.LimitReader(os.Stdin, 1)); cancel() }()
		if err := fsdurability.Tree(ctx, os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "Camp staging synchronization failed:", err)
			os.Exit(1)
		}
		if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"schema": 1, "synchronized": true}); err != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 3 && os.Args[1] == "native-client" {
		code, err := nativejob.Client(ctx, os.Args[2], os.Stdin, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Camp native client failed:", err)
			code = 1
		}
		os.Exit(code)
	}
	if len(os.Args) == 3 && os.Args[1] == "native-job" {
		if err := nativejob.Worker(ctx, os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, "Camp native process owner failed:", err)
			os.Exit(1)
		}
		return
	}
	if err := sandboxexec.Run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "Camp sandbox helper failed:", err)
		os.Exit(1)
	}
}
