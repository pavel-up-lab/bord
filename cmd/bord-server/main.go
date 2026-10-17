// Command bord-server is the server and administration entrypoint.
package main

import (
	"context"
	"os"

	"bord/internal/operations/command"
)

func main() {
	os.Exit(command.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
