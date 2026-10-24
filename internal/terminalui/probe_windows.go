//go:build windows

package terminalui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func readProbeResponse(ctx context.Context, input *os.File) ([]byte, error) {
	var response bytes.Buffer
	buffer := make([]byte, 256)
	for response.Len() < 4096 {
		timeout, err := windowsProbeTimeout(ctx)
		if err != nil {
			return response.Bytes(), err
		}
		status, err := windows.WaitForSingleObject(windows.Handle(input.Fd()), timeout)
		if err != nil {
			return response.Bytes(), err
		}
		if status == uint32(windows.WAIT_TIMEOUT) {
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

func windowsProbeTimeout(ctx context.Context) (uint32, error) {
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
	milliseconds := (remaining + time.Millisecond - 1) / time.Millisecond
	if milliseconds > time.Duration(^uint32(0)-1) {
		milliseconds = time.Duration(^uint32(0) - 1)
	}
	return uint32(milliseconds), nil
}
