.PHONY: build install lint test test-integration cover fmt tidy clean release-snapshot release-check test-install

BINARY := things

build:
	go build -o $(BINARY) ./cmd/things

install:
	go install ./cmd/things

lint:
	golangci-lint run ./...

test:
	go test -race ./...

# Integration tests (build tag `integration`) build the binary and exercise it
# end-to-end, e.g. the MCP stdio round-trip. Kept out of the default `test`.
test-integration:
	go test -tags integration -race ./...

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
