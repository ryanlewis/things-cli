.PHONY: build install lint test cover fmt tidy clean release-snapshot release-check test-install

BINARY := things

# Stamp the same variables goreleaser sets (see .goreleaser.yaml) so a local
# build reports which commit it came from instead of "dev (commit none)".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/things

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/things

lint:
	golangci-lint run ./...

test:
	go test -race ./...

cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

fmt:
	gofmt -w .
	go tool goimports -w . 2>/dev/null || true

tidy:
	go mod tidy

clean:
	rm -f $(BINARY) coverage.out
	rm -rf dist/

release-check:
	goreleaser check

release-snapshot:
	goreleaser release --snapshot --clean --skip=publish

test-install:
	./scripts/test-install.sh
