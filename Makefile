# bfeed — build/test/lint. Pure Go (CGO_ENABLED=0), single binary at ./cmd/bfeed.
BINARY  := bfeed
PKG     := github.com/bcrisp4/bfeed
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

GOBIN   := $(shell go env GOPATH)/bin
# Use golangci-lint from PATH if present, else the go-installed copy in
# GOPATH/bin (run `make tools` to install it). Avoids "No such file" when
# GOPATH/bin is not on PATH.
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo $(GOBIN)/golangci-lint)

GOLANGCI_VERSION := v2.12.2
SQLC_VERSION     := v1.31.1

.PHONY: all build build-linux-amd64 build-linux-arm64 test test-race lint fmt \
        vet tidy sqlc sqlc-check migrate run image tools clean

all: lint test build

build: ## Build for the host arch
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed

build-linux-amd64:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed

build-linux-arm64:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/bfeed

test: ## Unit tests
	go test ./...

test-race: ## Unit tests with the race detector
	go test -race ./...

lint: ## golangci-lint (same config CI uses)
	$(GOLANGCI) run

fmt: ## Apply gofumpt + goimports via golangci
	$(GOLANGCI) fmt

vet:
	go vet ./...

tidy:
	go mod tidy

sqlc: ## Regenerate sqlc code (after editing queries/ or migrations/)
	sqlc generate

sqlc-check: ## Fail if committed sqlc code is stale (CI parity)
	sqlc generate && git diff --exit-code internal/store/sqlite/sqlc

migrate: ## Apply DB migrations (BFEED_BASE_URL is required by the config loader, unused here)
	BFEED_BASE_URL=http://localhost:8080 go run ./cmd/bfeed migrate

run: ## Run locally (BFEED_BASE_URL is required)
	BFEED_LISTEN_ADDR=:8080 BFEED_BASE_URL=http://localhost:8080 BFEED_LOG_FORMAT=text \
		go run ./cmd/bfeed serve

image: ## Build the container image locally with docker (host arch, dev Dockerfile)
	docker build -t $(BINARY):$(VERSION) .

tools: ## Install pinned dev tools into GOPATH/bin (golangci-lint, sqlc)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)

clean:
	rm -f $(BINARY)
