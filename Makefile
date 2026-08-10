SERVICE := distribution

.PHONY: run build test check

run:
	CONFIG_PATH=config.example.toml go run ./cmd/distribution

build:
	go build ./...

test:
	go test ./...

check:
	gofmt -w cmd internal
	go vet ./...
	go test ./...
