// A launchd qualification child, never part of a Camp package.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) != 5 || os.Args[1] != "runner" || os.Args[2] != "run" || os.Args[3] != "--data-dir" {
		os.Exit(2)
	}
	dir := os.Args[4]
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := os.WriteFile(filepath.Join(dir, "service-started"), []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
		os.Exit(3)
	}
	<-ctx.Done()
	if err := os.WriteFile(filepath.Join(dir, "service-stopped"), []byte("graceful"), 0600); err != nil {
		os.Exit(4)
	}
}
