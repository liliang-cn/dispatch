# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

dispatch is a simple SSH batch operation tool written in Go that executes commands, copies files, and fetches files from multiple remote servers in parallel. It can be used as a CLI tool or Go library.

## Build and Development

### Go Version
- Requires Go 1.21 or later

### Build Commands

```bash
# Build CLI to bin/
make build

# Clean build artifacts
make clean

# Run tests
make test

# Install CLI tool globally
go install github.com/liliang-cn/dispatch/cmd/dispatch@latest
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
   - Falls back to reading `~/.ssh/config` if TOML config doesn't exist
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

### Entry Points

- **CLI**: `cmd/dispatch/main.go` - Uses Cobra framework, commands: exec, file (send/get/update/delete), hosts, config
- **Library**: `pkg/dispatch/client.go` - Public API

### Configuration File Format

Located at `~/.dispatch/config.toml` (optional - dispatch reads `~/.ssh/config` by default):

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
- `github.com/spf13/cobra` - CLI framework
- `golang.org/x/crypto/ssh` - SSH client

### CLI Commands

- `dispatch exec --hosts <group> -- <command>` - Execute commands on hosts
- `dispatch file send --src <local> --dest <remote> --hosts <group>` - Send files to hosts
- `dispatch file get --src <remote> --dest <local> --hosts <group>` - Get files from hosts
- `dispatch file update --src <local> --dest <remote> --hosts <group>` - Update files only if changed
- `dispatch file delete --path <remote> --hosts <group>` - Delete files on hosts
- `dispatch hosts` - List configured hosts and groups
- `dispatch config` - Open configuration file
