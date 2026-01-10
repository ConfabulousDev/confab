.PHONY: build clean test

BUILD_TIME := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

build:
	go build -ldflags "-X main.date=$(BUILD_TIME)" -o confab

clean:
	rm -f confab

test:
	go test ./...
