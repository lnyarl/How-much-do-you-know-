.PHONY: build test check

build:
	go build -o bin/penu ./cmd/penu

test:
	go test ./...

check:
	go test -race ./...
	go vet ./...
