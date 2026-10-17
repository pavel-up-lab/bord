// Package command implements operations-only bord-server subcommands.
package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"bord/internal/operations/backup"
	"bord/internal/operations/schema"
	storesqlite "bord/internal/storage/sqlite"
)

const (
	ExitOK           = 0
	ExitFailure      = 1
	ExitUsage        = 2
	ExitIncompatible = 3
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return ExitUsage
	}
	switch args[0] {
	case "migrate":
		return runSchema(ctx, args[1:], stdout, stderr, true)
	case "status":
		return runSchema(ctx, args[1:], stdout, stderr, false)
	case "backup":
		return runBackup(ctx, args[1:], stdout, stderr)
	case "restore-verify":
		return runVerify(args[1:], stdout, stderr)
	case "restore":
		return runRestore(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "bord-server %s commit=%s built=%s\n", Version, Commit, BuildDate)
		return ExitOK
	default:
		fmt.Fprintln(stderr, "unknown command")
		usage(stderr)
		return ExitUsage
	}
}

func runSchema(ctx context.Context, args []string, stdout, stderr io.Writer, migrate bool) int {
	name := "status"
	if migrate {
		name = "migrate"
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", envOr("BORD_DATABASE", "bord.db"), "SQLite database path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return ExitUsage
	}
	db, err := storesqlite.Open(*database)
	if err != nil {
		fmt.Fprintf(stderr, "%s failed: %v\n", name, err)
		return ExitFailure
	}
	defer db.Close()
	var status schema.Status
	if migrate {
		status, err = schema.Migrate(ctx, db)
	} else {
		status, err = schema.Inspect(ctx, db)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s failed: %v\n", name, err)
		if errors.Is(err, schema.ErrNewerSchema) {
			return ExitIncompatible
		}
		return ExitFailure
	}
	if err := json.NewEncoder(stdout).Encode(status); err != nil {
		fmt.Fprintf(stderr, "write status: %v\n", err)
		return ExitFailure
	}
	return ExitOK
}

func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", envOr("BORD_DATABASE", "bord.db"), "SQLite database path")
	blobs := flags.String("blobs", envOr("BORD_BLOBS", "data/blobs"), "blob directory")
	output := flags.String("output", "", "new backup directory")
	offline := flags.Bool("offline", false, "confirm the service is stopped")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *output == "" || !*offline {
		fmt.Fprintln(stderr, "backup requires --output and --offline")
		return ExitUsage
	}
	manifest, err := backup.Create(ctx, backup.Config{Database: *database, Blobs: *blobs, Destination: *output, Quiescer: backup.Offline{}})
	if err != nil {
		fmt.Fprintf(stderr, "backup failed: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "backup verified manifest_format=%d files=%d\n", manifest.Format, len(manifest.Blobs)+1)
	return ExitOK
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("restore-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("backup", "", "backup directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *root == "" {
		return ExitUsage
	}
	manifest, err := backup.Verify(*root)
	if err != nil {
		fmt.Fprintf(stderr, "restore verification failed: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "restore verification passed manifest_format=%d files=%d\n", manifest.Format, len(manifest.Blobs)+1)
	return ExitOK
}

func runRestore(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("restore", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("backup", "", "backup directory")
	destination := flags.String("destination", "", "new restore directory")
	offline := flags.Bool("offline", false, "confirm the service is stopped")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *root == "" || *destination == "" || !*offline {
		fmt.Fprintln(stderr, "restore requires --backup, --destination, and --offline")
		return ExitUsage
	}
	if err := backup.Restore(*root, *destination); err != nil {
		fmt.Fprintf(stderr, "restore failed: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, "restore completed; run status and readiness checks before cutover")
	return ExitOK
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: bord-server <migrate|status|backup|restore-verify|restore|version> [options]")
}
