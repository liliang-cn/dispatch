---
name: dispatch
description: SSH batch operation tool for executing commands, copying files, and managing multiple remote servers in parallel. Use when you need to run commands on multiple hosts, transfer files, or manage SSH-based infrastructure operations.
---

# dispatch

dispatch is a Go-based SSH batch operation tool that executes commands, copies files, and fetches files from multiple remote servers in parallel. It can be used as a CLI tool, Go library, or gRPC server.

## When to Use

Use dispatch when you need to:
- Execute commands on multiple remote servers in parallel
- Copy files to or from multiple hosts
- Perform rolling updates or deployments
- Gather information from multiple servers
- Manage infrastructure at scale

## Configuration

dispatch reads configuration from multiple sources with the following priority (highest to lowest):

1. `~/.dispatch/config.toml` - Host-level settings
2. `~/.dispatch/config.toml` - Group-level settings
3. `~/.ssh/config` - SSH config entries (User, Port, IdentityFile, HostName)
4. Default values (current user, port 22, auto-detect SSH keys)

This means dispatch works out of the box if you have `~/.ssh/config` configured.

## CLI Commands

### Execute Commands

```bash
# Execute on hosts from SSH config
dispatch exec --hosts web1,web2 -- uptime

# Execute on host group from config.toml
dispatch exec --hosts web -- systemctl status nginx

# Execute with options
dispatch exec --hosts db --parallel 5 --timeout 60 -- pg_dump -U postgres mydb

# Execute with environment variables
dispatch exec --hosts app --env APP_ENV=production -- ./deploy.sh

# Execute in specific directory
dispatch exec --hosts app --dir /app -- git pull
```

### File Operations

```bash
# Send file to hosts
dispatch file send --src ./app.conf --dest /etc/app/app.conf --hosts web

# Get file from hosts
dispatch file get --src /var/log/app.log --dest ./logs/ --hosts web

# Update file only if changed (with backup)
dispatch file update --src ./config.yaml --dest /etc/app/config.yaml --hosts app --backup

# Delete file on hosts
dispatch file delete --path /tmp/oldfile --hosts web
```

### Host Management

```bash
# List all configured hosts
dispatch hosts

# Open config file for editing
dispatch config
```

## Go Library Usage

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/liliang-cn/dispatch/pkg/dispatch"
)

func main() {
    // Create client with default config (reads from ~/.ssh/config)
    client, err := dispatch.New(nil)
    if err != nil {
        log.Fatal(err)
    }

    ctx := context.Background()

    // Execute command on multiple hosts
    result, err := client.Exec(ctx, []string{"web1", "web2"}, "uptime",
        dispatch.WithTimeout(30*time.Second),
        dispatch.WithParallel(5),
    )
    if err != nil {
        log.Fatal(err)
    }

    // Process results
    for host, r := range result.Hosts {
        if r.Success {
            fmt.Printf("[%s] %s\n", host, r.Output)
        } else {
            fmt.Printf("[%s] Error: %s\n", host, r.Error)
        }
    }

    // Copy file to hosts
    copyResult, err := client.Copy(ctx, []string{"web"}, "./app.conf", "/etc/app/app.conf",
        dispatch.WithCopyMode(0644),
    )

    // Fetch file from hosts
    fetchResult, err := client.Fetch(ctx, []string{"db"}, "/var/log/postgres.log", "./logs/")
}
```

## gRPC Server

dispatch can run as a gRPC server for remote management:

```bash
# Start server
dispatch-server --port 50051 --config ~/.dispatch/config.toml

# Use grpcurl to interact
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext localhost:50051 v1.Dispatch/Exec
```

## Common Patterns

### Deployment

```bash
# Deploy application with zero-downtime
dispatch exec --hosts app -- systemctl stop myapp
dispatch file send --src ./myapp --dest /usr/local/bin/myapp --hosts app
dispatch exec --hosts app -- systemctl start myapp
```

### Rolling Update

```bash
# Update one host at a time
for host in web1 web2 web3; do
    dispatch exec --hosts $host -- systemctl restart nginx
    sleep 10
done
```

### Log Collection

```bash
# Collect logs from all servers
dispatch file get --src /var/log/syslog --dest ./logs/ --hosts all
```

## Host Selection

Hosts can be specified in several ways:
- Direct hostnames from `~/.ssh/config`: `web1`, `db1`
- IP addresses: `192.168.1.10`
- Group names from `config.toml`: `web`, `db`
- Wildcard patterns: `web*`, `app-*`

## Resources

- GitHub: https://github.com/liliang-cn/dispatch
- Documentation: See CLAUDE.md in the repository
- Configuration: `~/.dispatch/config.toml`
- SSH Config: `~/.ssh/config`
