.PHONY: all build clean test install

BIN_DIR := bin
CLI_BIN := $(BIN_DIR)/dispatch
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(CLI_BIN) ./cmd/dispatch

clean:
	rm -rf $(BIN_DIR)

test:
	go test ./...

install:
	install -m 755 $(CLI_BIN) /usr/local/bin/dispatch
