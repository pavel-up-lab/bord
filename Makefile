.PHONY: fmt lint test test-race generate contract build terminal-smoke

fmt:
	gofmt -w $$(find . -name '*.go')

lint:
	@test -z "$$(gofmt -l $$(find . -name '*.go'))"
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

generate:
	go tool sqlc generate
	go generate ./...

contract: generate
	git diff --exit-code -- api/generated/client.gen.go internal/storage/sqlite/dbgen
	go test ./api/contract

build:
	go build ./...

terminal-smoke:
	./scripts/terminal-smoke.sh
