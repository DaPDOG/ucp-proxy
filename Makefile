.PHONY: fmt lint test build clean install-hooks

# Format all Go files
fmt:
	gofmt -w .

# Check formatting without modifying (fails if unformatted)
lint:
	@test -z "$$(gofmt -l .)" || (echo "Unformatted files:"; gofmt -l .; echo "Run 'make fmt'"; exit 1)

# Run tests (depends on lint passing)
test: lint
	go test ./... -count=1

# Build all packages (depends on lint passing)
build: lint
	go build ./...

# Clean build artifacts
clean:
	go clean ./...

# Install git pre-commit hook
install-hooks:
	@cp scripts/pre-commit .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo "Pre-commit hook installed"
