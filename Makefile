.PHONY: help build test test-integration test-api lint fmt clean check

# Default target
help:
	@echo "Available targets:"
	@echo "  build              - Build the CLI tool"
	@echo "  test               - Run all tests"
	@echo "  test-integration   - Run integration tests (requires -tags=integration)"
	@echo "  test-api          - Run API integration tests"
	@echo "  lint               - Run golangci-lint"
	@echo "  fmt                - Format code"
	@echo "  vet                - Run go vet"
	@echo "  check              - Run all checks (fmt, vet, lint, test)"
	@echo "  clean              - Remove build artifacts"

# Build
build:
	go build -o ssh-multihop ./cmd/ssh-multihop

# Development build with verbose output
dev:
	go build -v -o ssh-multihop ./cmd/ssh-multihop

# Run all tests (quiet mode by default)
test:
	go test ./... -v

# Run tests with verbose logging (for debugging)
test-verbose:
	DEBUG_TESTS=1 go test ./... -v

# Run integration tests
test-integration:
	go test ./internal/api -tags=integration -v -timeout 30s
	go test ./internal/cli -tags=integration -v

# Run API tests
test-api:
	go test ./internal/api -tags=integration -v -timeout 30s

# Run linter
lint:
	golangci-lint run

# Auto-fix lint issues
lint-fix:
	golangci-lint run --fix

# Format code
fmt:
	go fmt ./...
	goimports -w .

# Run go vet
vet:
	go vet ./...

# Run all checks (fail fast on any error)
check:
	@$(MAKE) fmt && $(MAKE) vet && $(MAKE) lint && $(MAKE) test

# Clean build artifacts
clean:
	rm -f ssh-multihop
	rm -f /tmp/ssh-multihop-fwd.db
	rm -f /tmp/ssh-multihop*.log 2>/dev/null || true
	go clean

# Install dependencies
deps:
	go mod download
	go mod tidy

# Run with coverage
coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
