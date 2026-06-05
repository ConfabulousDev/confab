.PHONY: build clean test check-deps

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

build:
	go build -ldflags "-X main.date=$(BUILD_TIME)" -o confab

clean:
	rm -f confab

check-deps:
	@echo "Checking required tooling..."
	@command -v go >/dev/null 2>&1 || { echo "Go is required: https://go.dev/dl/"; exit 1; }
	@echo "  go: found"
	@command -v npx >/dev/null 2>&1 || { echo "Node.js/npx is required: https://nodejs.org/"; exit 1; }
	@echo "  npx: found"
	@echo "All dependencies found."

test: check-deps
	go test ./...
