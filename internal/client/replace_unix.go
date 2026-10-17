//go:build !windows

package client

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
