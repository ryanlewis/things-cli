.PHONY: build lint test cover fmt tidy clean

BINARY := things

build:
	go build -o $(BINARY) .

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
