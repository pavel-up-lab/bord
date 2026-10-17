// Command bord is the terminal client entrypoint.
package main

import (
	"fmt"
	"os"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("bord %s commit=%s built=%s\n", version, commit, buildDate)
		return
	}
}
