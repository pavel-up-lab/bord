//go:build linux

package diskusage

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

type Space struct {
	TotalBytes, AvailableBytes int64
}

func Usage(path string) (Space, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return Space{}, err
	}
	blockSize := uint64(stats.Bsize)
	if blockSize == 0 || stats.Blocks > math.MaxInt64/blockSize || stats.Bavail > math.MaxInt64/blockSize {
		return Space{}, errors.New("filesystem capacity overflow")
	}
	return Space{
		TotalBytes:     int64(stats.Blocks * blockSize),
		AvailableBytes: int64(stats.Bavail * blockSize),
	}, nil
}
