// Command bord-server is the server and administration entrypoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"bord/internal/api"
	"bord/internal/events"
	"bord/internal/operations/command"
	"bord/internal/platform/config"
	"bord/internal/platform/logging"
	"bord/internal/server"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "serve" || args[0] == "-config" || args[0] == "--config" {
		if len(args) > 0 && args[0] == "serve" {
			args = args[1:]
		}
		if err := serve(ctx, args, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "bord-server:", err)
			return command.ExitFailure
		}
		return command.ExitOK
	}
	if len(args) == 1 && args[0] == "--version" {
		args[0] = "version"
	}
	return command.Run(ctx, args, stdout, stderr)
}

func serve(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("usage: bord-server serve [--config path]")
	}
	cfg, err := config.Load(*path, os.Getenv)
	if err != nil {
		return err
	}
	logger := logging.JSON(stdout, slog.LevelInfo)
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
	logger.Info("server starting", "event", "server.start", "public_address", cfg.HTTP.Address, "management_address", managementAddress)
	return service.Run(ctx)
}
