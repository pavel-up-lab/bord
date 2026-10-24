//go:build !windows

package terminalui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func readProbeResponse(ctx context.Context, input *os.File) ([]byte, error) {
	var response bytes.Buffer
	buffer := make([]byte, 256)
	for response.Len() < 4096 {
		timeout, err := probePollTimeout(ctx)
		if err != nil {
			return response.Bytes(), err
		}
		fds := []unix.PollFd{{Fd: int32(input.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, timeout)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return response.Bytes(), err
		}
		if ready == 0 {
			return response.Bytes(), context.DeadlineExceeded
		}
		n, readErr := input.Read(buffer)
		if n > 0 {
			response.Write(buffer[:n])
			if probeResponseComplete(response.Bytes()) {
				return response.Bytes(), nil
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return response.Bytes(), nil
			}
			return response.Bytes(), readErr
		}
	}
	return response.Bytes(), nil
}

func probePollTimeout(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return 150, nil
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return 0, context.DeadlineExceeded
	}
	milliseconds := int((remaining + time.Millisecond - 1) / time.Millisecond)
	return max(1, milliseconds), nil
}
