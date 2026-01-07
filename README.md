# dispatch

A simple SSH batch operation tool for executing commands and managing files across multiple servers.

> Ansible too complex? Just need to run commands and copy files to multiple machines? Use dispatch.

## Features

- **Batch Command Execution** - Execute shell commands on multiple hosts simultaneously
- **File Operations** - Send, fetch, update, and delete remote files
- **TUI Mode** - Visual table interface for multi-host operations
- **Real-time Output** - Streaming output for long-running commands
- **Host Key Verification** - SSH known_hosts support with auto-add mode
- **Connection Reuse** - Efficient connection handling within operations
- **Configuration Priority** - TOML config > ~/.ssh/config > /etc/hosts
- **SSH Agent Support** - Automatic authentication method fallback
- **Host Groups** - Organize hosts into groups for batch operations
- **Parallel Execution** - Configurable concurrency control
- **Colored Logging** - Debug, info, warn, error levels with optional timestamps
- **gRPC Server** - Run as a service for programmatic access

## Installation

```bash
go install github.com/liliang-cn/dispatch/cmd/dispatch@latest
```

Or build from source:

```bash
git clone https://github.com/liliang-cn/dispatch
cd dispatch
make build
sudo mv bin/dispatch /usr/local/bin/
```

## Configuration

Create `~/.dispatch/config.toml`:

```toml
# SSH default settings
[ssh]
user = "root"
port = 22
key_path = "~/.ssh/id_rsa"
timeout = "30s"
known_hosts = "~/.ssh/known_hosts"   # Host key verification (empty = disable)
strict_host_key = false               # true = reject unknown hosts, false = auto-add

# Execution settings
[exec]
parallel = 10              # Default parallel connections
timeout = "5m"             # Command timeout
shell = "/bin/bash"        # Remote shell

# Logging
[log]
level = "info"             # debug, info, warn, error
output = "stdout"          # stdout, stderr, or file path
no_color = false           # Disable colored output
show_time = false          # Show timestamps

# Host groups
[hosts.web]
addresses = [
    "192.168.1.10",
    "192.168.1.11",
    "192.168.1.12"
]
user = "www-data"          # Override default user for this group

[hosts.db]
addresses = ["192.168.1.20", "192.168.1.21"]

[hosts.all]
addresses = [
    "192.168.1.10", "192.168.1.11", "192.168.1.12",
    "192.168.1.20", "192.168.1.21"
]
```

## Configuration Priority

dispatch respects configuration from multiple sources in the following order:

1. **TOML host-level config** (highest priority)
2. **TOML group-level config**
3. **~/.ssh/config**
4. **/etc/hosts** (for hostname resolution fallback)

This means if you set `user = "admin"` in your TOML file for a host, it will override the `User` setting in `~/.ssh/config`.

## Usage

### Execute Commands

```bash
# Execute on host group (uses TUI mode for multiple hosts)
dispatch exec --hosts web -- "uptime"

# Execute on multiple hosts
dispatch exec --hosts "host1,host2" -- "systemctl status nginx"

# Use text mode instead of TUI
dispatch exec --hosts web --no-tui -- "apt-get install nginx"

# Execute interactive commands (inject stdin)
dispatch exec --hosts web --input "y\n" -- "./interactive_script.sh"

# Set parallelism and timeout
dispatch exec --hosts web -p 5 -t 60 -- "df -h"
```

### File Operations

```bash
# Send file to multiple hosts (uses TUI for progress)
dispatch file send -s ./nginx.conf -d /etc/nginx/nginx.conf --hosts web

# Send with custom permissions and backup existing file
dispatch file send -s app.conf -d /etc/app/app.conf --hosts web --mode 644 --backup

# Use text mode instead of TUI
dispatch file send -s ./app.conf -d /etc/app/app.conf --hosts web --no-tui

# Fetch file from multiple hosts
dispatch file get -s /var/log/app.log -d ./logs/ --hosts web

# Update file (only copies when changed)
dispatch file update -s nginx.conf -d /etc/nginx/nginx.conf --hosts web --backup

# Delete remote file
dispatch file delete --path /tmp/old.log --hosts web
```

### Other Commands

```bash
# List all hosts and groups
dispatch hosts
```

## Global Options

| Flag | Description |
|------|-------------|
| `-c, --config` | Config file path (default: `~/.dispatch/config.toml`) |
| `-p, --parallel` | Parallel connections (default: from config) |
| `-t, --timeout` | Timeout in seconds (default: from config) |
| `--log-level` | Log level: debug, info, warn, error |
| `--no-tui` | Disable TUI mode, use text output |

### TUI Mode

When operating on multiple hosts, dispatch automatically uses TUI (Terminal UI) mode:

- **exec**: Shows real-time command execution status in a table
- **file send/get/update**: Shows file transfer progress

To disable TUI and use plain text output, add `--no-tui`:

```bash
dispatch exec --hosts web --no-tui -- "uptime"
dispatch file send -s file.txt -d /tmp/file.txt --hosts web --no-tui
```

## Authentication

dispatch supports multiple authentication methods with automatic fallback:

1. **ssh-agent** - Tried first if `SSH_AUTH_SOCK` is set
2. **Specified key** - Key from TOML config or `--key` option
3. **Default keys** - Tries `id_rsa`, `id_ed25519`, `id_ecdsa`, etc. from `~/.ssh/`

### Using ~/.ssh/config

dispatch automatically reads your SSH config:

```ssh
# ~/.ssh/config
Host production
    HostName 192.168.1.100
    User deploy
    Port 2222
    IdentityFile ~/.ssh/deploy_key
```

```bash
# Use the alias from SSH config
dispatch exec --hosts production -- "uname -a"
```

### Using /etc/hosts

If DNS resolution fails, dispatch falls back to `/etc/hosts`:

```
# /etc/hosts
192.168.1.10  web1
192.168.1.11  web2
192.168.1.12  web3
```

## Go Library Usage

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/liliang-cn/dispatch/pkg/dispatch"
)

func main() {
    // Create client with default config
    client, _ := dispatch.New(nil)

    ctx := context.Background()

    // Execute command on multiple hosts
    result, _ := client.Exec(ctx, []string{"web"}, "uname -a",
        dispatch.WithParallel(5),
        dispatch.WithTimeout(30*time.Second),
    )

    // Execute interactive command with input
    client.Exec(ctx, []string{"web"}, "./script.sh",
        dispatch.WithInput("y\n"),
    )

    for _, r := range result.Hosts {
        if r.Success {
            fmt.Printf("[%s] %s\n", r.Host, r.Output)
        }
    }
}
```

### File Operations

```go
// Send file to multiple hosts with backup
copyResult, _ := client.Copy(ctx, []string{"web"}, "./app.conf", "/etc/app/app.conf",
    dispatch.WithCopyMode(0644),
    dispatch.WithParallel(5),
    dispatch.WithBackup(true),
)

// Fetch file from multiple hosts
fetchResult, _ := client.Fetch(ctx, []string{"web"}, "/var/log/app.log", "./logs/")

// Update file (only copy when changed) with backup
updateResult, _ := client.Update(ctx, []string{"web"}, "./app.conf", "/etc/app/app.conf",
    dispatch.WithUpdateBackup(true),
)
```

## gRPC Server

dispatch can run as a gRPC server for programmatic access:

```bash
# Start server
dispatch-server --port 50051

# Or build and run
make build
./bin/dispatch-server --port 50051
```

### gRPC API

| Method | Description |
|--------|-------------|
| `Exec` | Stream command execution results |
| `Copy` | Stream file copy progress |
| `Fetch` | Stream file fetch results |
| `Hosts` | List configured hosts and groups |
| `ListJobs` | List all jobs with status |
| `GetJob` | Get job details by ID |
| `CancelJob` | Cancel a running job |

## Project Structure

```
dispatch/
├── cmd/
│   ├── dispatch/          # CLI application
│   └── dispatch-server/   # gRPC server
├── pkg/
│   ├── dispatch/          # High-level client API
│   ├── executor/          # Parallel execution engine
│   ├── inventory/         # TOML configuration & host management
│   ├── logger/            # Colored logging
│   ├── server/            # gRPC server implementation
│   ├── ssh/               # SSH client with host key verification
│   └── tui/               # Terminal UI components
├── proto/
│   └── dispatch.proto     # gRPC service definition
└── config/
    └── example.toml       # Example configuration
```

## Comparison with Ansible

| Feature | Ansible | dispatch |
|---------|---------|----------|
| Learning curve | Steep | Gentle |
| Config format | YAML Playbooks | TOML |
| Execution | Modules | Direct commands |
| Integration | CLI/Python | Go library/CLI/gRPC |
| Best for | Full automation | Daily ops/Go integration |
| Real-time output | Limited | Yes (TUI mode) |
| Host key verification | Yes | Yes (known_hosts) |

## License

MIT
