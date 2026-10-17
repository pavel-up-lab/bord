// Package admin contains transport-independent administrative command handlers.
package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var ErrUsage = errors.New("invalid admin command usage")

type Report struct {
	ID, PostID, Reason string
	CreatedAt          time.Time
}

// Client is the management API boundary. Command handlers never access a DB.
type Client interface {
	Bootstrap(context.Context, string, string, time.Duration) (string, error)
	RevokeIdentity(context.Context, string, string) error
	ListReports(context.Context) ([]Report, error)
	Hide(context.Context, string, string) error
	Restore(context.Context, string, string) error
	Close(context.Context, string, string) error
	CreateBan(context.Context, string, time.Duration, string) error
	RevokeBan(context.Context, string, string) error
	RevealIP(context.Context, string, string) (string, error)
}

type Runner struct {
	client Client
	output io.Writer
}

func NewRunner(client Client, output io.Writer) *Runner {
	return &Runner{client: client, output: output}
}

func (r *Runner) Execute(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return ErrUsage
	}
	switch args[0] {
	case "bootstrap":
		if len(args) != 4 {
			return ErrUsage
		}
		ttl, err := time.ParseDuration(args[3])
		if err != nil || ttl <= 0 {
			return ErrUsage
		}
		credential, err := r.client.Bootstrap(ctx, args[1], args[2], ttl)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(r.output, credential)
		return err
	case "revoke":
		if len(args) != 4 || args[2] != "--reason" || args[3] == "" {
			return ErrUsage
		}
		return r.client.RevokeIdentity(ctx, args[1], args[3])
	case "list-reports":
		if len(args) != 1 {
			return ErrUsage
		}
		reports, err := r.client.ListReports(ctx)
		if err != nil {
			return err
		}
		for _, report := range reports {
			if _, err := fmt.Fprintf(r.output, "%s\t%s\t%s\t%s\n", report.ID, report.PostID, report.Reason, report.CreatedAt.UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
		return nil
	case "hide", "restore", "close":
		target, reason, err := targetReason(args)
		if err != nil {
			return err
		}
		if args[0] == "hide" {
			return r.client.Hide(ctx, target, reason)
		}
		if args[0] == "restore" {
			return r.client.Restore(ctx, target, reason)
		}
		return r.client.Close(ctx, target, reason)
	case "ban":
		if len(args) != 5 || args[3] != "--reason" || strings.TrimSpace(args[4]) == "" {
			return ErrUsage
		}
		ttl, err := time.ParseDuration(args[2])
		if err != nil || ttl <= 0 {
			return ErrUsage
		}
		return r.client.CreateBan(ctx, args[1], ttl, args[4])
	case "unban":
		target, reason, err := targetReason(args)
		if err != nil {
			return err
		}
		return r.client.RevokeBan(ctx, target, reason)
	case "reveal":
		target, reason, err := targetReason(args)
		if err != nil {
			return err
		}
		ip, err := r.client.RevealIP(ctx, target, reason)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(r.output, ip)
		return err
	default:
		return ErrUsage
	}
}

func targetReason(args []string) (string, string, error) {
	if len(args) != 4 || args[2] != "--reason" || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[3]) == "" {
		return "", "", ErrUsage
	}
	return args[1], args[3], nil
}
