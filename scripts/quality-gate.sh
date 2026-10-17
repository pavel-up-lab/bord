#!/bin/sh
set -eu

# Keep the hardening gate reproducible on developer machines and CI. Callers may
# raise these bounds, but the defaults intentionally finish in a few minutes.
FUZZTIME=${FUZZTIME:-5s}
REPEAT=${REPEAT:-5}

go test ./...
go test -race ./...

# Re-run the concurrency- and boundary-sensitive paths under the race detector.
go test -race ./internal/api ./internal/events ./internal/media ./internal/client ./internal/server \
	-run 'StrictJSON|JSONResponse|SSE|Subscribe|Hub|Normalize|Cancellation|Shutdown' \
	-count="$REPEAT"

# Go permits one fuzz target per invocation. Both targets enforce their own
# input/resource bounds and include the regression corpus as seeds.
go test ./internal/client -run '^$' \
	-fuzz '^FuzzParseSSEFragmentationIDsAndMultiline$' -fuzztime="$FUZZTIME"
go test ./internal/media -run '^$' \
	-fuzz '^FuzzNormalize$' -fuzztime="$FUZZTIME"

make contract
go vet ./...
