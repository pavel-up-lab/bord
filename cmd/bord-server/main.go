// Command bord-server is the server and administration entrypoint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bord/internal/api"
	"bord/internal/events"
	"bord/internal/integration"
	"bord/internal/media"
	"bord/internal/operations/command"
	"bord/internal/platform/config"
	"bord/internal/platform/diskusage"
	"bord/internal/platform/logging"
	"bord/internal/server"
	"bord/internal/storage/sqlite"
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
	db, err := sqlite.Open(cfg.Storage.Database)
	if err != nil {
		return err
	}
	if err := sqlite.Migrate(ctx, db); err != nil {
		_ = db.Close()
		return err
	}
	runtime, err := integration.New(integration.Config{DB: db, Blobs: cfg.Storage.Blobs, LookupKey: cfg.Secrets.IPLookupKey, EncryptionKey: cfg.Secrets.IPEncryptionKey, EncryptionKeyID: cfg.Secrets.IPEncryptionKeyID, PreviousEncryptionKey: cfg.Secrets.IPPreviousEncryptionKey, PreviousEncryptionKeyID: cfg.Secrets.IPPreviousEncryptionKeyID, StoreRawIP: cfg.Privacy.StoreRawIP})
	if err != nil {
		_ = db.Close()
		return err
	}
	if err := runtime.SeedBoard(ctx, "general", "General"); err != nil {
		_ = runtime.Close()
		return err
	}
	if err := runtime.Ready(ctx); err != nil {
		_ = runtime.Close()
		return err
	}
	maintenance, err := runtime.Maintain(ctx)
	if err != nil {
		_ = runtime.Close()
		return fmt.Errorf("startup maintenance: %w", err)
	}
	logger.Info("maintenance completed", "event", "maintenance.completed", "retention_ip", maintenance.Retention.IP, "retention_idempotency", maintenance.Retention.Idempotency, "retention_challenges", maintenance.Retention.Challenges, "media_removed", maintenance.Media.Removed, "media_bytes", maintenance.Media.Bytes)
	cursors, err := api.NewCursorCodec(cfg.Secrets.IPLookupKey, nil)
	if err != nil {
		_ = runtime.Close()
		return err
	}
	lifecycle := &server.Lifecycle{}
	hub := events.NewHub(512, 32)
	trusted, err := trustedProxyNetworks(os.Getenv("BORD_TRUSTED_PROXIES"))
	if err != nil {
		_ = hub.Close()
		_ = runtime.Close()
		return err
	}
	mediaLimits := media.DefaultLimits()
	mediaLimits.MaxWidth, mediaLimits.MaxHeight, mediaLimits.MaxPixels = 6000, 6000, 16_000_000
	apiConfig := api.Config{
		Public:         integration.Public{Runtime: runtime},
		Media:          integration.Media{Runtime: runtime, Processor: media.Processor{Limits: mediaLimits}},
		Abuse:          runtime.Abuse,
		Admin:          runtime.Admin,
		Logger:         logger,
		CursorCodec:    cursors,
		Events:         hub,
		Readiness:      []api.ReadinessProbe{lifecycle, api.ReadinessProbeFunc(runtime.Ready)},
		TrustedProxies: trusted,
		MaxBodyBytes:   cfg.HTTP.MaxBodyBytes,
		DiskPath:       cfg.Storage.Blobs,
		DiskUsage: func(path string) (api.DiskSpace, error) {
			space, err := diskusage.Usage(path)
			return api.DiskSpace{TotalBytes: space.TotalBytes, AvailableBytes: space.AvailableBytes}, err
		},
	}
	public := api.NewPublicRouter(apiConfig)
	managementAddress := os.Getenv("BORD_MANAGEMENT_ADDRESS")
	management := api.NewManagementRouter(apiConfig)
	service, err := server.New(server.Config{
		PublicAddress:     cfg.HTTP.Address,
		ManagementAddress: managementAddress,
		PublicHandler:     public,
		ManagementHandler: management,
		ReadTimeout:       cfg.HTTP.BodyReadTimeout,
		StreamClosers:     []io.Closer{hub},
		Closers:           []io.Closer{runtime},
	}, lifecycle)
	if err != nil {
		_ = hub.Close()
		_ = runtime.Close()
		return err
	}
	backgroundCtx, cancelBackground := context.WithCancel(ctx)
	dispatcher := &events.Dispatcher{Outbox: runtime.Outbox, Hub: hub, Logger: logger}
	go func() {
		if err := dispatcher.Run(backgroundCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("outbox dispatcher stopped", "event", "outbox.stopped", "error", err)
		}
	}()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-backgroundCtx.Done():
				return
			case <-ticker.C:
				maintenance, err := runtime.Maintain(backgroundCtx)
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("maintenance failed", "event", "maintenance.failed", "error", err)
				} else if err == nil {
					logger.Info("maintenance completed", "event", "maintenance.completed", "retention_ip", maintenance.Retention.IP, "retention_idempotency", maintenance.Retention.Idempotency, "retention_challenges", maintenance.Retention.Challenges, "media_removed", maintenance.Media.Removed, "media_bytes", maintenance.Media.Bytes)
				}
			}
		}
	}()
	logger.Info("server starting", "event", "server.start", "public_address", cfg.HTTP.Address, "management_address", managementAddress)
	runErr := service.Run(ctx)
	cancelBackground()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return errors.Join(runErr, service.Shutdown(cleanupCtx))
}

func trustedProxyNetworks(value string) ([]*net.IPNet, error) {
	var result []*net.IPNet
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		_, network, err := net.ParseCIDR(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy CIDR %q", raw)
		}
		result = append(result, network)
	}
	return result, nil
}
