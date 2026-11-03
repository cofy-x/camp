package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/eventflow"
	"github.com/cofy-x/camp/apps/server/internal/httpapi"
	"github.com/cofy-x/camp/apps/server/internal/library"
	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/serviceowner"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"github.com/cofy-x/camp/apps/server/internal/team"
	"github.com/cofy-x/camp/apps/server/internal/webui"
)

// Config contains resolved service settings. AllowedHosts adds hostnames or IPs
// without ports; TrustedOrigins contains complete browser origins.
type Config struct {
	TLSCert             string
	TLSKey              string
	AllowFixtureRunners bool
	MaxConcurrentRuns   int
	PublicOrigin        string
	ImportRoots         []string
	Addr                string
	DataDir             string
	AllowedHosts        []string
	TrustedOrigins      []string
	Logger              *slog.Logger
	ShutdownTimeout     time.Duration
	Models              configuration.ModelInput
	OnReady             func(address, setupPath string)
}

// Run serves the desktop until its caller cancels ctx or the listener fails.
func Run(ctx context.Context, config Config) error {
	if (config.TLSCert == "") != (config.TLSKey == "") {
		return errors.New("TLS certificate and key must be configured together")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	// Validate policy before creating a database or claiming the listener.
	importPolicy, err := configuration.NewImportPolicy(config.ImportRoots)
	if err != nil {
		return err
	}
	policy, err := newPolicy(config)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", config.Addr, err)
	}
	defer listener.Close()
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// Claim the existing execution fence before migrations can modify live data.
	ownership, err := serviceowner.Acquire(config.DataDir)
	if err != nil {
		return fmt.Errorf("claim execution ownership: %w", err)
	}
	defer ownership.Close()
	store, err := storage.OpenContext(ctx, filepath.Join(config.DataDir, "desktop.sqlite"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer store.Close()
	auth, err := team.Open(ctx, store, config.DataDir)
	if err != nil {
		return fmt.Errorf("initialize team authentication: %w", err)
	}
	setupPath := ""
	if required, err := auth.SetupRequired(ctx); err != nil {
		return err
	} else if required {
		setupPath = auth.SetupPath()
		logger.Info("Camp setup required", "token_file", auth.SetupPath())
	}
	runManager, err := remoteruns.New(ctx, store, remoteruns.Config{DataDir: config.DataDir, Models: config.Models, AllowFixture: config.AllowFixtureRunners, Concurrency: config.MaxConcurrentRuns}, logger)
	if err != nil {
		return fmt.Errorf("initialize agent runs: %w", err)
	}
	defer runManager.Shutdown(context.Background())
	recoverLegacyDocumentJobs(ctx, config.DataDir, logger)
	rooms, err := collaboration.New(ctx, store, runManager, config.DataDir)
	if err != nil {
		return fmt.Errorf("initialize rooms: %w", err)
	}
	mux := http.NewServeMux()
	api := httpapi.New(store, logger, rooms)
	webhookSecrets, err := configuration.OpenWebhookSecrets(filepath.Join(config.DataDir, "configuration", "github-webhooks"))
	if err != nil {
		return fmt.Errorf("initialize webhook credentials: %w", err)
	}
	api.EnableGitHub(webhookSecrets)
	commentCredentials, err := configuration.OpenGitHubCredentials(filepath.Join(config.DataDir, "configuration", "github-comments"))
	if err != nil {
		return fmt.Errorf("initialize comment credentials: %w", err)
	}
	events, err := eventflow.New(ctx, store, runManager.ModelConfiguration(), commentCredentials, logger)
	if err != nil {
		return fmt.Errorf("initialize event processing: %w", err)
	}
	defer events.Shutdown(context.Background())
	api.EnableAutomation(commentCredentials)
	api.EnableTeam(auth, config.TrustedOrigins, config.PublicOrigin)
	api.EnableRemote(runManager)
	api.EnableSettings(runManager, importPolicy)
	api.EnableLibrary(library.New(store, resources.New(filepath.Join(config.DataDir, "resources")), runManager))
	defer api.StopStreams()
	mux.Handle("/api/daemon/", runManager.Handler())
	mux.Handle("/api/", api)
	mux.Handle("/", webui.Handler())
	requests, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()
	httpServer := &http.Server{
		Addr:              config.Addr,
		Handler:           policy.handler(mux),
		BaseContext:       func(net.Listener) context.Context { return requests },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	done := make(chan error, 1)
	go func() {
		if config.TLSCert != "" {
			done <- httpServer.ServeTLS(listener, config.TLSCert, config.TLSKey)
		} else {
			done <- httpServer.Serve(listener)
		}
	}()
	scheme := "http://"
	if config.TLSCert != "" {
		scheme = "https://"
	}
	logger.Info("desktop listening", "url", scheme+listener.Addr().String(), "data", config.DataDir)
	if config.OnReady != nil {
		config.OnReady(listener.Addr().String(), setupPath)
	}
	var serveErr error
	select {
	case serveErr = <-done:
	case <-ctx.Done():
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	budget := config.ShutdownTimeout
	if budget <= 0 {
		budget = 10 * time.Second
	}
	shutdown, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	api.StopStreams()
	return errors.Join(serveErr, shutdownService(shutdown, httpServer, func(ctx context.Context) error {
		// Stop intake effects before the execution scheduler drains. Persisted
		// uncertain work is reconciled on restart, never replayed at shutdown.
		eventErr := events.Shutdown(ctx)
		return errors.Join(eventErr, runManager.Shutdown(ctx))
	}, cancelRequests))
}

func shutdownService(ctx context.Context, server *http.Server, stopTerminals func(context.Context) error, cancelRequests context.CancelFunc) error {
	terminalDone := make(chan error, 1)
	go func() { terminalDone <- stopTerminals(ctx) }()
	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil {
		cancelRequests()
		_ = server.Close()
	}
	return errors.Join(shutdownErr, <-terminalDone)
}
