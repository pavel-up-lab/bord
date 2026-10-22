//go:build windows

package adminclient

import "os"

func validateCredentialFileOwner(os.FileInfo) error { return nil }
