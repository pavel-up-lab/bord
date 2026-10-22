//go:build !windows

package adminclient

import (
	"errors"
	"os"
	"syscall"
)

func validateCredentialFileOwner(info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("admin credential file permissions must be 0600")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("admin credential file must belong to the current user")
	}
	return nil
}
