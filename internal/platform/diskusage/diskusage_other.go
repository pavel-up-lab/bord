//go:build !linux

package diskusage

import "errors"

type Space struct {
	TotalBytes, AvailableBytes int64
}

func Usage(string) (Space, error) {
	return Space{}, errors.New("filesystem capacity is unsupported")
}
