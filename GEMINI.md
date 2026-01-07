# dispatch

**Project Overview**

`dispatch` is a high-performance SSH batch operation tool written in Go. It allows you to execute commands and manage files across multiple servers simultaneously. It can be used as a standalone Command Line Interface (CLI), a Go library for embedding into other applications, or a gRPC server for programmatic access.

**Key Features:**
*   **Batch Execution:** Run shell commands on groups of hosts in parallel.
*   **File Management:** Send (`copy`), fetch (`get`), update (`update`), and delete files.
*   **Smart Updates:** The `update` command only transfers files if the content has changed (using SHA256 checksums).
*   **Safety First:** Supports file backups (`--backup`) before overwriting.
*   **Streaming Output:** Real-time output streaming for long-running commands.
*   **Interactive Support:** Inject standard input (`--input`) for interactive commands.
*   **Host Key Verification:** Secure connection handling with `known_hosts` support.
*   **Flexible Config:** Supports TOML configuration, `~/.ssh/config`, and `/etc/hosts`.

## Architecture

The project is structured into clear layers:

*   **`cmd/`**: Entry points for the applications.
    *   `dispatch/`: The CLI tool.
    *   `dispatch-server/`: The gRPC server.
*   **`pkg/`**: Core logic libraries.
    *   `dispatch/`: Public-facing Go client API.
    *   `executor/`: The heart of the system; handles concurrency, connection caching, and task orchestration.
    *   `inventory/`: Manages host configurations, groups, and overrides (TOML parsing).
    *   `ssh/`: Low-level SSH client wrapper, handling connections, authentication, and `known_hosts` verification.
    *   `server/`: gRPC server implementation.
*   **`proto/`**: Protocol Buffer definitions (`dispatch.proto`) for the gRPC service.

## Building and Running

This project uses a `Makefile` for common tasks.

### Prerequisites
*   Go 1.21 or later
*   `protoc` (if regenerating gRPC code)

### Commands

*   **Build All:**
    ```bash
    make build
    # Binaries will be placed in bin/dispatch and bin/dispatch-server
    ```

*   **Build Components:**
    ```bash
    make cli      # Build only CLI
    make server   # Build only Server
    ```

*   **Run Tests:**
    ```bash
    make test
    # or
    go test ./...
    ```

*   **Regenerate gRPC Code:**
    ```bash
    make proto
    ```

*   **Clean:**
    ```bash
    make clean
    ```

## Configuration

Configuration is primarily handled via `~/.dispatch/config.toml`.

**Example:**
```toml
[ssh]
user = "root"
port = 22
key_path = "~/.ssh/id_rsa"
known_hosts = "~/.ssh/known_hosts"
strict_host_key = false  # true = reject unknown, false = auto-add

[exec]
parallel = 10

[hosts.web]
addresses = ["10.0.0.1", "10.0.0.2"]
```

## Development Conventions

*   **Language:** Go (Golang).
*   **Style:** Follows standard Go idioms. Use `go fmt` before committing.
*   **Testing:** Unit tests are located alongside source code (e.g., `executor_test.go`). The `pkg/executor` package has extensive mock-based tests for core logic.
*   **gRPC:** Changes to the API should be made in `proto/dispatch.proto` and then regenerated using `make proto`.
*   **Error Handling:** Use wrapped errors (`fmt.Errorf("%w", err)`) to preserve context.
*   **Concurrency:** The `executor` uses `sync.WaitGroup` and channels to manage parallel SSH connections safely.
