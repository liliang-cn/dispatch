# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

dispatch is a simple SSH batch operation tool written in Go that executes commands, copies files, and fetches files from multiple remote servers in parallel. It can be used as a CLI tool, Go library, or gRPC server.

## Build and Development

### Go Version
- Requires Go 1.21 or later

### Build Commands

```bash
# Build all (CLI + server) to bin/
make build

# Build only CLI
make cli

# Build only server
make server

# Clean build artifacts
make clean

# Run tests
make test

# Regenerate protobuf code
make proto

# Install CLI tool globally
go install github.com/liliang-cn/dispatch/cmd/dispatch@latest

# Install gRPC server globally
go install github.com/liliang-cn/dispatch/cmd/dispatch-server@latest
```

### gRPC Code Generation
The proto file is at `proto/dispatch.proto`. To regenerate Go code from proto:
```bash
make proto
# or
protoc --go_out=. --go-grpc_out=. proto/dispatch.proto
```

## Architecture

The codebase follows a layered architecture with clear separation of concerns:

### Layer Structure

1. **API Layer** (`pkg/dispatch/`)
   - Main client API providing public interface for both CLI and library usage
   - Defines `Config`, `SSHConfig`, `ExecConfig` and functional options like `WithTimeout`, `WithEnv`, `WithDir`, `WithParallel`
   - Entry point: `dispatch.New(config)` returns a client

2. **Configuration Layer** (`pkg/inventory/`)
   - Parses TOML configuration from `~/.dispatch/config.toml` by default
   - Host groups with inheritance: defaults → group overrides → host overrides
   - Path expansion for `~` in key paths

3. **Execution Layer** (`pkg/executor/`)
   - Handles parallel execution across multiple hosts
   - Callback-based result delivery for real-time processing
   - Context-aware cancellation and timeout handling
   - Connection reuse within single operations (lightweight, not full pooling)

4. **Transport Layer** (`pkg/ssh/`)
   - Low-level SSH client using `golang.org/x/crypto/ssh`
   - Supports key-based and password authentication
   - SCP-based file transfers
   - ssh-agent integration
   - Host key verification with known_hosts format (`KnownHostsVerifier`)
   - AutoAdd mode (default): adds new host keys automatically
   - Strict mode: rejects unknown hosts

5. **Server Layer** (`pkg/server/`)
   - gRPC server implementation with streaming responses
   - Job-based execution with tracking (Exec, Copy, Fetch operations)
   - Concurrent job handling with status monitoring

### Entry Points

- **CLI**: `cmd/dispatch/main.go` - Uses Cobra framework, commands: exec, file (send/get/update/delete), hosts, config
- **gRPC Server**: `cmd/dispatch-server/main.go` - Configurable port and config path
- **Library**: `pkg/dispatch/client.go` - Public API

### Configuration File Format

Located at `~/.dispatch/config.toml`:

```toml
[ssh]
user = "root"
port = 22
key_path = "~/.ssh/id_rsa"
timeout = 30
known_hosts = "~/.ssh/known_hosts"   # Host key verification (empty = disable)
strict_host_key = false               # true = reject unknown hosts, false = auto-add

[exec]
parallel = 10
timeout = 300
shell = "/bin/bash"

[hosts.web]
addresses = ["192.168.1.10", "192.168.1.11"]

[hosts.web.ssh]
user = "admin"  # Override default user for this group

[hosts.db]
addresses = ["192.168.1.20"]
```

### Key Patterns

1. **Functional Options**: The client API uses functional options (`WithTimeout`, `WithEnv`, `WithDir`, `WithParallel`) for configuration
2. **Callback Pattern**: Results delivered via callbacks in executor for streaming output
3. **Host Selection**: Hosts specified as group names (string) or individual addresses (slice)
4. **Result Aggregation**: Operations return result structs with per-host status tracking
5. **Connection Reuse**: Executor caches connections within single operations, releasing them after completion (not persistent pooling)

### Dependencies

- `github.com/BurntSushi/toml` - TOML configuration parsing
- `github.com/spf13/cobra` + `viper` - CLI framework
- `github.com/charmbracelet/bubbletea` + `lipgloss` - TUI components (included but not actively used in CLI)
- `golang.org/x/crypto/ssh` - SSH client
- `google.golang.org/grpc` - gRPC framework

### CLI Commands

- `dispatch exec --hosts <group> -- <command>` - Execute commands on hosts
- `dispatch file send --src <local> --dest <remote> --hosts <group>` - Send files to hosts
- `dispatch file get --src <remote> --dest <local> --hosts <group>` - Get files from hosts
- `dispatch file update --src <local> --dest <remote> --hosts <group>` - Update files only if changed
- `dispatch file delete --path <remote> --hosts <group>` - Delete files on hosts
- `dispatch hosts` - List configured hosts and groups
- `dispatch config` - Open configuration file

### gRPC Service

Defined in `proto/dispatch.proto`:
- `Exec` - Stream command execution
- `Copy` - Stream file copying
- `Fetch` - Stream file fetching
- `Hosts` - Get host information
- `GetJob`, `ListJobs`, `CancelJob` - Job management
