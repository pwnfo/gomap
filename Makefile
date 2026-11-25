BINARY      := gomap
PKG         := github.com/pwnfo/gomap
CMD_DIR     := ./cmd/gomap
BIN_DIR     := ./bin
VERSION     ?= 1.0.0
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo "")
LDFLAGS     := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)

.PHONY: all build install test race bench coverage lint clean cross-build

all: build

build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD_DIR)

install:
	CGO_ENABLED=0 go install -trimpath -ldflags "$(LDFLAGS)" $(CMD_DIR)

test:
	go test -v ./...

race:
	go test -race ./...

bench:
	go test -bench=. -benchmem ./pkg/gomap

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

cross-build:
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 $(CMD_DIR)
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 $(CMD_DIR)
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64 $(CMD_DIR)
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 $(CMD_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-windows-amd64.exe $(CMD_DIR)

clean:
	rm -rf $(BIN_DIR) dist $(BINARY) coverage.out coverage.html
