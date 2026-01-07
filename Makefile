.PHONY: all build clean test proto install-cli install-server

BIN_DIR := bin
CLI_BIN := $(BIN_DIR)/dispatch
SERVER_BIN := $(BIN_DIR)/dispatch-server
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"

all: build

build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(CLI_BIN) ./cmd/dispatch
	go build $(LDFLAGS) -o $(SERVER_BIN) ./cmd/dispatch-server

cli:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(CLI_BIN) ./cmd/dispatch

server:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(SERVER_BIN) ./cmd/dispatch-server

clean:
	rm -rf $(BIN_DIR)

test:
	go test ./...

proto:
	protoc --go_out=. --go-grpc_out=. proto/dispatch.proto

install-cli: cli
	go install github.com/liliang-cn/dispatch/cmd/dispatch@latest

install-server: server
	go install github.com/liliang-cn/dispatch/cmd/dispatch-server@latest
