// Command bord-server is the server and administration entrypoint.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bord/internal/api"
	"bord/internal/events"
	"bord/internal/platform/config"
	"bord/internal/platform/logging"
	"bord/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "bord-server:", err)
		os.Exit(1)
	}
}

func run() error {
	path := ""
	if len(os.Args) == 3 && os.Args[1] == "-config" {
		path = os.Args[2]
	} else if len(os.Args) != 1 {
		return fmt.Errorf("usage: bord-server [-config path]")
	}
	cfg, err := config.Load(path, os.Getenv)
	if err != nil {
		return err
	}
	logger := logging.JSON(os.Stdout, slog.LevelInfo)
	cursors, err := api.NewCursorCodec(cfg.Secrets.IPLookupKey, nil)
	if err != nil {
		return err
	}
	lifecycle := &server.Lifecycle{}
	hub := events.NewHub(512, 32)
	apiConfig := api.Config{
		Logger:       logger,
		CursorCodec:  cursors,
		Events:       hub,
		Readiness:    []api.ReadinessProbe{lifecycle},
		MaxBodyBytes: cfg.HTTP.MaxBodyBytes,
	}
	public := api.NewPublicRouter(apiConfig)
	managementAddress := os.Getenv("BORD_MANAGEMENT_ADDRESS")
	management := api.NewManagementRouter(apiConfig)
	service, err := server.New(server.Config{
		PublicAddress:     cfg.HTTP.Address,
		ManagementAddress: managementAddress,
		PublicHandler:     public,
		ManagementHandler: management,
		Closers:           []io.Closer{hub},
	}, lifecycle)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("server starting", "event", "server.start", "public_address", cfg.HTTP.Address, "management_address", managementAddress)
	return service.Run(ctx)
}
