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

Configuration file location: `~/.dispatch/config.toml`

### Quick Example

```toml
[ssh]
user = "root"
port = 22
key_path = "~/.ssh/id_rsa"

[hosts.web]
addresses = ["192.168.1.10", "192.168.1.11"]
user = "www-data"
```

### Complete Configuration Reference

```toml
# ============================================
# SSH Default Settings
# ============================================
[ssh]
user           = "root"              # Default SSH user
port           = 22                  # Default SSH port
key_path       = "~/.ssh/id_rsa"     # Default private key path
timeout        = "30s"               # Connection timeout
known_hosts    = "~/.ssh/known_hosts" # Host key verification (empty = disable)
strict_host_key = false              # true = reject unknown hosts, false = auto-add

# ============================================
# Execution Settings
# ============================================
[exec]
parallel = 10        # Default parallel connections (default: 10)
timeout  = "5m"      # Command timeout (default: 5 minutes)
shell    = "/bin/bash" # Remote shell (default: /bin/bash)

# ============================================
# Logging Settings
# ============================================
[log]
level     = "info"   # debug, info, warn, error (default: info)
output    = "stdout" # stdout, stderr, or file path
no_color  = false    # Disable colored output (default: false)
show_time = false    # Show timestamps (default: false)

# ============================================
# Host Groups
# ============================================
# Group with multiple hosts, same user
[hosts.web]
addresses = ["192.168.1.10", "192.168.1.11", "192.168.1.12"]
user      = "www-data"   # Override default user for this group
port      = 22           # Override default port (optional)
key_path  = "~/.ssh/web_key" # Override default key (optional)

# Group with default settings
[hosts.db]
addresses = ["192.168.1.20", "192.168.1.21"]

# Single host (useful for per-host override)
[hosts.api-server]
addresses = ["192.168.1.100"]
user      = "api"
key_path  = "~/.ssh/api_key"
port      = 2222
```

### Per-Host Configuration

You can configure individual hosts with different users, ports, or keys:

```toml
[ssh]
user = "root"
key_path = "~/.ssh/id_rsa"

# Different user for web servers
[hosts.web]
addresses = ["web1", "web2", "web3"]
user = "www-data"
port = 22

# Different user and key for database servers
[hosts.db]
addresses = ["db1", "db2"]
user = "dbadmin"
key_path = "~/.ssh/db_key"

# Override specific host within a group
[hosts.web1]
user = "admin"      # This overrides the group's user
key_path = "~/.ssh/admin_key"
```

### Using SSH Aliases

You can use SSH config aliases as addresses:

```toml
# Assuming ~/.ssh/config has:
# Host gui01
#     HostName 192.168.123.117
#     User ubuntu

[hosts.gui]
addresses = ["gui01", "gui02", "gui03"]
# user will be read from ~/.ssh/config
```

### Wildcard Support

Use wildcards to match hosts from your SSH config:

```bash
# Match all hosts starting with "gui"
dispatch exec --hosts "gui*" -- "uptime"

# Match all hosts starting with "web"
dispatch exec --hosts "web*" -- "systemctl status nginx"
```

### Configuration Priority

When multiple sources define settings for a host, dispatch uses this priority (highest to lowest):

1. **TOML host-level** - `[hosts.hostname]` (highest)
2. **TOML group-level** - `[hosts.groupname]`
3. **~/.ssh/config** - SSH config file
4. **Default values** - from `[ssh]` section (lowest)

Example:

```toml
[ssh]
user = "root"          # Default user
port = 22              # Default port

[hosts.web]
addresses = ["web1", "web2"]
user = "www-data"      # Group override

[hosts.web1]
user = "admin"         # Host override (highest priority)
```

For `web1`:
- User: `admin` (from host-level)
- Port: `22` (from default)

For `web2`:
- User: `www-data` (from group-level)
- Port: `22` (from default)

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

# Check file stats (size, mode, owner, etc.) - uses TUI mode
dispatch file stats --path /etc/nginx/nginx.conf --hosts web

# Read text file content from remote hosts
dispatch file read --path /var/log/app.log --hosts web

# Read with offset and limit (for pagination)
dispatch file read --path /var/log/app.log --hosts web --offset 1000 --limit 500
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
- **file stats**: Shows file information (size, mode, owner, etc.) in a table

#### Exec Command

![dispatch exec](images/exec.png)

#### File Send Command

![dispatch file send progress](images/file.png)

![dispatch file send complete](images/file2.png)

To disable TUI and use plain text output, add `--no-tui`:

```bash
dispatch exec --hosts web --no-tui -- "uptime"
dispatch file send -s file.txt -d /tmp/file.txt --hosts web --no-tui
dispatch file stats --path /etc/hosts --hosts web --no-tui
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

// Get file stats (size, mode, owner, group, modtime)
statsResult, _ := client.Stats(ctx, []string{"web"}, "/etc/nginx/nginx.conf",
    dispatch.WithStatsParallel(5),
)

// Read text file content from remote hosts
readResult, _ := client.Read(ctx, []string{"web"}, "/var/log/app.log",
    dispatch.WithReadParallel(5),
    dispatch.WithReadOffset(1000),  // Optional: start reading from byte offset
    dispatch.WithReadLimit(500),    // Optional: limit bytes to read
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
| `Stats` | Get file stats (size, mode, owner, etc.) |
| `Read` | Read text file content from remote hosts |
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
