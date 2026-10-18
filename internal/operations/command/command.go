// Package command implements operations-only bord-server subcommands.
package command

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"bord/internal/demo"
	"bord/internal/operations/backup"
	"bord/internal/operations/operator"
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
	case "admin":
		return runAdmin(ctx, args[1:], stdout, stderr)
	case "retention":
		return runRetention(ctx, args[1:], stdout, stderr)
	case "ip-reencrypt":
		return runReencrypt(ctx, args[1:], stdout, stderr)
	case "demo-seed":
		return runDemoSeed(ctx, args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "bord-server %s commit=%s built=%s\n", Version, Commit, BuildDate)
		return ExitOK
	default:
		fmt.Fprintln(stderr, "unknown command")
		usage(stderr)
		return ExitUsage
	}
}

func runDemoSeed(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("demo-seed", flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", "", "SQLite database path (required; no production default)")
	blobs := flags.String("blobs", "", "blob directory (required; no production default)")
	reset := flags.Bool("reset", false, "remove and recreate only the deterministic demo fixture")
	force := flags.Bool("force", false, "required with --reset")
	confirm := flags.String("confirm", "", "DEMO-SEED, or RESET-DEMO-SEED with --reset")
	want := "DEMO-SEED"
	if err := flags.Parse(args); err != nil {
		return ExitUsage
	}
	if *reset {
		want = "RESET-DEMO-SEED"
	}
	if flags.NArg() != 0 || *database == "" || *blobs == "" || *confirm != want || (*reset && !*force) || (!*reset && *force) {
		fmt.Fprintln(stderr, "demo-seed requires explicit --database, --blobs, and --confirm DEMO-SEED; reset additionally requires --reset --force --confirm RESET-DEMO-SEED")
		return ExitUsage
	}
	db, err := storesqlite.Open(*database)
	if err != nil {
		fmt.Fprintln(stderr, "demo-seed failed:", err)
		return ExitFailure
	}
	defer db.Close()
	if err := storesqlite.Migrate(ctx, db); err != nil {
		fmt.Fprintln(stderr, "demo-seed failed:", err)
		return ExitFailure
	}
	result, err := demo.Seed(ctx, demo.Config{DB: db, Blobs: *blobs, Reset: *reset})
	if err != nil {
		fmt.Fprintln(stderr, "demo-seed failed:", err)
		return ExitFailure
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, "demo-seed failed:", err)
		return ExitFailure
	}
	return ExitOK
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

func openReady(ctx context.Context, database string) (*sql.DB, error) {
	db, err := storesqlite.Open(database)
	if err != nil {
		return nil, err
	}
	if err := storesqlite.Ready(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runAdmin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "admin requires create, rotate, or revoke")
		return ExitUsage
	}
	name := args[0]
	flags := flag.NewFlagSet("admin "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", envOr("BORD_DATABASE", "bord.db"), "SQLite database path")
	id := flags.String("id", "", "administrator identity")
	offline := flags.Bool("offline", false, "confirm service is stopped")
	switch name {
	case "create", "rotate":
		role := flags.String("role", "", "reports, moderator, or administrator")
		ttl := flags.Duration("ttl", 0, "credential lifetime")
		output := flags.String("credential-output", "", "new exclusive credential file")
		confirm := flags.String("confirm", "", "confirmation token")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !*offline || *id == "" || *ttl <= 0 || *output == "" || (name == "rotate" && *confirm != "ROTATE") {
			fmt.Fprintln(stderr, "admin create/rotate requires --offline --id --role --ttl --credential-output; rotate also requires --confirm ROTATE")
			return ExitUsage
		}
		parsed, err := operator.ParseRole(*role)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return ExitUsage
		}
		db, err := openReady(ctx, *database)
		if err != nil {
			fmt.Fprintln(stderr, "admin failed:", err)
			return ExitFailure
		}
		defer db.Close()
		if err := operator.CreateCredential(ctx, db, *id, parsed, *ttl, *output, name == "rotate", time.Now().UTC()); err != nil {
			fmt.Fprintln(stderr, "admin failed:", err)
			return ExitFailure
		}
		fmt.Fprintf(stdout, "administrator %s completed id=%s credential_file=%s\n", name, *id, *output)
		return ExitOK
	case "revoke":
		reason := flags.String("reason", "", "non-sensitive audit reason")
		confirm := flags.String("confirm", "", "confirmation token")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !*offline || *id == "" || *reason == "" || *confirm != "REVOKE" {
			fmt.Fprintln(stderr, "admin revoke requires --offline --id --reason and --confirm REVOKE")
			return ExitUsage
		}
		db, err := openReady(ctx, *database)
		if err != nil {
			fmt.Fprintln(stderr, "admin failed:", err)
			return ExitFailure
		}
		defer db.Close()
		if err := operator.RevokeCredential(ctx, db, *id, *reason, time.Now().UTC()); err != nil {
			fmt.Fprintln(stderr, "admin failed:", err)
			return ExitFailure
		}
		fmt.Fprintf(stdout, "administrator revoked id=%s\n", *id)
		return ExitOK
	case "ban", "unban":
		managementURL := flags.String("management-url", "", "management API base URL")
		credentialFile := flags.String("credential-file", "", "0600 bearer credential file")
		reason := flags.String("reason", "", "non-sensitive audit reason")
		if name == "ban" {
			subjectType := flags.String("subject-type", "", "ip, prefix, or lookup")
			subject := flags.String("subject", "", "ban subject")
			operations := flags.String("operations", "", "comma-separated operations")
			ttl := flags.Duration("ttl", 0, "ban lifetime")
			if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *managementURL == "" || *credentialFile == "" || *subjectType == "" || *subject == "" || *operations == "" || *ttl <= 0 || *reason == "" {
				fmt.Fprintln(stderr, "admin ban requires management URL, credential file, subject type/value, operations, ttl, and reason")
				return ExitUsage
			}
			credential, err := operator.ReadCredentialFile(*credentialFile)
			if err != nil {
				fmt.Fprintln(stderr, "admin ban:", err)
				return ExitFailure
			}
			banID, err := operator.ManagementBan(ctx, http.DefaultClient, *managementURL, credential, operator.BanRequest{SubjectType: *subjectType, Subject: *subject, Operations: strings.Split(*operations, ","), ExpiresAt: time.Now().UTC().Add(*ttl), Reason: *reason})
			if err != nil {
				fmt.Fprintln(stderr, "admin ban:", err)
				return ExitFailure
			}
			fmt.Fprintf(stdout, "ban created id=%s\n", banID)
			return ExitOK
		}
		banID := flags.String("ban-id", "", "ban id")
		confirm := flags.String("confirm", "", "confirmation token")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *managementURL == "" || *credentialFile == "" || *banID == "" || *reason == "" || *confirm != "REVOKE-BAN" {
			fmt.Fprintln(stderr, "admin unban requires management URL, credential file, ban id, reason, and --confirm REVOKE-BAN")
			return ExitUsage
		}
		credential, err := operator.ReadCredentialFile(*credentialFile)
		if err != nil {
			fmt.Fprintln(stderr, "admin unban:", err)
			return ExitFailure
		}
		if err := operator.ManagementUnban(ctx, http.DefaultClient, *managementURL, credential, *banID, *reason); err != nil {
			fmt.Fprintln(stderr, "admin unban:", err)
			return ExitFailure
		}
		fmt.Fprintf(stdout, "ban revoked id=%s\n", *banID)
		return ExitOK
	default:
		fmt.Fprintln(stderr, "admin requires create, rotate, or revoke")
		return ExitUsage
	}
}

func runRetention(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "retention requires report, run, hold, or release")
		return ExitUsage
	}
	name := args[0]
	flags := flag.NewFlagSet("retention "+name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", envOr("BORD_DATABASE", "bord.db"), "SQLite database path")
	offline := flags.Bool("offline", false, "confirm service is stopped")
	if name == "report" {
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return ExitUsage
		}
		db, err := openReady(ctx, *database)
		if err != nil {
			fmt.Fprintln(stderr, "retention failed:", err)
			return ExitFailure
		}
		defer db.Close()
		report, err := operator.InspectRetention(ctx, db, time.Now().UTC())
		if err != nil {
			fmt.Fprintln(stderr, "retention failed:", err)
			return ExitFailure
		}
		json.NewEncoder(stdout).Encode(report)
		return ExitOK
	}
	if name == "hold" || name == "release" {
		subject := flags.Int64("subject-id", 0, "request subject id")
		confirm := flags.String("confirm", "", "confirmation token")
		want := "HOLD"
		if name == "release" {
			want = "RELEASE"
		}
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !*offline || *subject <= 0 || *confirm != want {
			fmt.Fprintf(stderr, "retention %s requires --offline --subject-id and --confirm %s\n", name, want)
			return ExitUsage
		}
		db, err := openReady(ctx, *database)
		if err != nil {
			fmt.Fprintln(stderr, "retention failed:", err)
			return ExitFailure
		}
		defer db.Close()
		if err := operator.SetLegalHold(ctx, db, *subject, name == "hold"); err != nil {
			fmt.Fprintln(stderr, "retention failed:", err)
			return ExitFailure
		}
		fmt.Fprintf(stdout, "legal hold %s subject_id=%d\n", name, *subject)
		return ExitOK
	}
	if name != "run" {
		fmt.Fprintln(stderr, "retention requires report, run, hold, or release")
		return ExitUsage
	}
	blobs := flags.String("blobs", envOr("BORD_BLOBS", "data/blobs"), "blob directory")
	backupRoot := flags.String("backup", "", "new pre-operation backup directory")
	confirm := flags.String("confirm", "", "confirmation token")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || !*offline || *backupRoot == "" || *confirm != "DELETE" {
		fmt.Fprintln(stderr, "retention run requires --offline --backup and --confirm DELETE")
		return ExitUsage
	}
	if _, err := backup.Create(ctx, backup.Config{Database: *database, Blobs: *blobs, Destination: *backupRoot, Quiescer: backup.Offline{}}); err != nil {
		fmt.Fprintln(stderr, "retention backup failed:", err)
		return ExitFailure
	}
	db, err := openReady(ctx, *database)
	if err != nil {
		fmt.Fprintln(stderr, "retention failed:", err)
		return ExitFailure
	}
	defer db.Close()
	report, err := operator.RunRetention(ctx, db, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, "retention failed:", err)
		return ExitFailure
	}
	json.NewEncoder(stdout).Encode(report)
	return ExitOK
}

func runReencrypt(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("ip-reencrypt", flag.ContinueOnError)
	flags.SetOutput(stderr)
	database := flags.String("database", envOr("BORD_DATABASE", "bord.db"), "SQLite database path")
	blobs := flags.String("blobs", envOr("BORD_BLOBS", "data/blobs"), "blob directory")
	backupRoot := flags.String("backup", "", "new pre-operation backup directory")
	fromID := flags.String("from-key-id", "", "current key id")
	fromFile := flags.String("from-key-file", "", "base64 current key file")
	toID := flags.String("to-key-id", "", "new key id")
	toFile := flags.String("to-key-file", "", "base64 new key file")
	batch := flags.Int("batch-size", 100, "transaction batch size")
	offline := flags.Bool("offline", false, "confirm service is stopped")
	confirm := flags.String("confirm", "", "confirmation token")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !*offline || *backupRoot == "" || *fromID == "" || *fromFile == "" || *toID == "" || *toFile == "" || *confirm != "REENCRYPT" {
		fmt.Fprintln(stderr, "ip-reencrypt requires --offline --backup, both key ids/files, and --confirm REENCRYPT")
		return ExitUsage
	}
	fromKey, err := operator.ReadKeyFile(*fromFile)
	if err != nil {
		fmt.Fprintln(stderr, "ip-reencrypt: invalid source key file")
		return ExitFailure
	}
	toKey, err := operator.ReadKeyFile(*toFile)
	if err != nil {
		fmt.Fprintln(stderr, "ip-reencrypt: invalid destination key file")
		return ExitFailure
	}
	if _, err := backup.Create(ctx, backup.Config{Database: *database, Blobs: *blobs, Destination: *backupRoot, Quiescer: backup.Offline{}}); err != nil {
		fmt.Fprintln(stderr, "ip-reencrypt backup failed:", err)
		return ExitFailure
	}
	db, err := openReady(ctx, *database)
	if err != nil {
		fmt.Fprintln(stderr, "ip-reencrypt failed:", err)
		return ExitFailure
	}
	defer db.Close()
	report, err := operator.Reencrypt(ctx, db, *fromID, fromKey, *toID, toKey, *batch)
	if err != nil {
		fmt.Fprintln(stderr, "ip-reencrypt failed without advancing the current batch:", err)
		return ExitFailure
	}
	json.NewEncoder(stdout).Encode(report)
	return ExitOK
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: bord-server <migrate|status|backup|restore-verify|restore|admin|retention|ip-reencrypt|demo-seed|version> [options]")
}
