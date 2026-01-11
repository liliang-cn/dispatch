package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Config represents the complete configuration for dispatch
type Config struct {
	SSH   SSHConfig            `toml:"ssh"`
	Exec  ExecConfig           `toml:"exec"`
	Log   LogConfig            `toml:"log"`
	Hosts map[string]HostGroup `toml:"hosts"`
}

// LogConfig contains logging configuration
type LogConfig struct {
	Level    string `toml:"level"`     // debug, info, warn, error
	Output   string `toml:"output"`    // stdout, stderr, or file path
	NoColor  bool   `toml:"no_color"`  // disable colored output
	ShowTime bool   `toml:"show_time"` // show timestamp
}

// SSHConfig contains default settings for SSH connections
type SSHConfig struct {
	User           string `toml:"user"`
	Port           int    `toml:"port"`
	KeyPath        string `toml:"key_path"`
	Timeout        string `toml:"timeout"`         // Parsed as duration
	KnownHostsPath string `toml:"known_hosts"`     // Path to known_hosts file
	StrictHostKey  bool   `toml:"strict_host_key"` // Strict host key checking (reject unknown hosts)
}

// ExecConfig contains default execution settings
type ExecConfig struct {
	Parallel int    `toml:"parallel"`
	Timeout  string `toml:"timeout"`
	Shell    string `toml:"shell"`
}

// HostGroup represents a host group
type HostGroup struct {
	Addresses []string `toml:"addresses"`
	User      string   `toml:"user"`
	Port      int      `toml:"port"`
	KeyPath   string   `toml:"key_path"`
}

// Host represents complete configuration for a single host
type Host struct {
	Address     string // Host address
	User        string // SSH user
	Port        int    // SSH port
	KeyPath     string // Private key path
	UserSet     bool   // Mark whether User is explicitly set
	PortSet     bool   // Mark whether Port is explicitly set
	KeyPathSet  bool   // Mark whether KeyPath is explicitly set
}

// Inventory manages host inventory
type Inventory struct {
	mu     sync.RWMutex
	config *Config
	path   string
}

// New creates a new Inventory
func New(configPath string) (*Inventory, error) {
	if configPath == "" {
		// Default config path
		home, _ := os.UserHomeDir()
		configPath = filepath.Join(home, ".dispatch", "config.toml")
	}

	inv := &Inventory{
		config: &Config{
			SSH: SSHConfig{
				User:           "", // Empty means use current system user
				Port:           22,
				KeyPath:        "", // Empty means try default key locations
				Timeout:        "30s",
				KnownHostsPath: "~/.ssh/known_hosts",
				StrictHostKey:  false, // Default to auto-add mode for compatibility
			},
			Exec: ExecConfig{
				Parallel: 10,
				Timeout:  "5m",
				Shell:    "/bin/bash",
			},
			Log: LogConfig{
				Level:    "info",
				Output:   "stdout",
				NoColor:  false,
				ShowTime: false,
			},
			Hosts: make(map[string]HostGroup),
		},
		path: configPath,
	}

	// Try to load configuration
	if _, err := os.Stat(configPath); err == nil {
		if err := inv.Load(); err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
	}

	return inv, nil
}

// Load loads configuration from file
func (inv *Inventory) Load() error {
	inv.mu.Lock()
	defer inv.mu.Unlock()

	data, err := os.ReadFile(inv.path)
	if err != nil {
		return err
	}

	config := &Config{
		Hosts: make(map[string]HostGroup),
	}

	if err := toml.Unmarshal(data, config); err != nil {
		return err
	}

	inv.config = config
	return nil
}

// Save saves configuration to file
func (inv *Inventory) Save() error {
	inv.mu.RLock()
	defer inv.mu.RUnlock()

	// Ensure directory exists
	dir := filepath.Dir(inv.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Use toml encoder
	var buf strings.Builder
	if err := toml.NewEncoder(&buf).Encode(inv.config); err != nil {
		return err
	}

	return os.WriteFile(inv.path, []byte(buf.String()), 0644)
}

// GetHosts gets hosts by group name or address list
func (inv *Inventory) GetHosts(patterns []string) ([]Host, error) {
	inv.mu.RLock()
	defer inv.mu.RUnlock()

	var hosts []Host
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		// Check if it's a defined group
		if group, ok := inv.config.Hosts[pattern]; ok {
			for _, addr := range group.Addresses {
				if !seen[addr] {
					hosts = append(hosts, inv.buildHost(addr, pattern))
					seen[addr] = true
				}
			}
		} else if strings.ContainsAny(pattern, "*?[") {
			// Expand wildcard from SSH config
			matches := ExpandWildcardFromSSHConfig(pattern)
			for _, match := range matches {
				if !seen[match] {
					hosts = append(hosts, inv.buildHost(match, ""))
					seen[match] = true
				}
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no hosts found for wildcard pattern: %s", pattern)
			}
		} else {
			// Treat as direct host address
			if !seen[pattern] {
				hosts = append(hosts, inv.buildHost(pattern, ""))
				seen[pattern] = true
			}
		}
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts found for patterns: %v", patterns)
	}

	return hosts, nil
}

// buildHost builds host configuration, merging defaults and overrides
// Priority: TOML host > TOML group > SSH config > defaults
func (inv *Inventory) buildHost(address string, group string) Host {
	host := Host{
		Address: address,
		User:    inv.config.SSH.User,
		Port:    inv.config.SSH.Port,
		KeyPath: inv.config.SSH.KeyPath,
		// Default values are not marked as explicitly set
		UserSet:    false,
		PortSet:    false,
		KeyPathSet: false,
	}

	// Check host-level overrides (highest priority)
	if hostConfig, ok := inv.config.Hosts[address]; ok {
		if hostConfig.User != "" {
			host.User = hostConfig.User
			host.UserSet = true
		}
		if hostConfig.Port != 0 {
			host.Port = hostConfig.Port
			host.PortSet = true
		}
		if hostConfig.KeyPath != "" {
			host.KeyPath = hostConfig.KeyPath
			host.KeyPathSet = true
		}
	}

	// Check group-level overrides (only applied if not set at host level)
	if group != "" {
		if groupConfig, ok := inv.config.Hosts[group]; ok {
			if !host.UserSet && groupConfig.User != "" {
				host.User = groupConfig.User
				host.UserSet = true
			}
			if !host.PortSet && groupConfig.Port != 0 {
				host.Port = groupConfig.Port
				host.PortSet = true
			}
			if !host.KeyPathSet && groupConfig.KeyPath != "" {
				host.KeyPath = groupConfig.KeyPath
				host.KeyPathSet = true
			}
		}
	}

	// Check SSH config (only applied if not set at TOML host/group level)
	if sshEntry, ok := GetSSHConfigEntry(address); ok {
		// Update Address if HostName is set in SSH config
		if sshEntry.HostName != "" {
			host.Address = sshEntry.HostName
		}
		if !host.UserSet && sshEntry.User != "" {
			host.User = sshEntry.User
		}
		if !host.PortSet && sshEntry.Port != 0 {
			host.Port = sshEntry.Port
		}
		if !host.KeyPathSet && sshEntry.KeyPath != "" {
			host.KeyPath = sshEntry.KeyPath
		}
	}

	return host
}

// GetAllGroups returns all groups
func (inv *Inventory) GetAllGroups() map[string][]string {
	inv.mu.RLock()
	defer inv.mu.RUnlock()

	groups := make(map[string][]string)
	for name, group := range inv.config.Hosts {
		groups[name] = group.Addresses
	}
	return groups
}

// GetDefaultParallel returns default parallel count
func (inv *Inventory) GetDefaultParallel() int {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return inv.config.Exec.Parallel
}

// GetDefaultTimeout returns default timeout in seconds
func (inv *Inventory) GetDefaultTimeout() int {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	// Simplified: default 300 seconds
	return 300
}

// ExpandPath expands ~ in path
func ExpandPath(path string) string {
	if path == "" {
		return path
	}

	if path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			return filepath.Join(home, path[2:])
		}
		return home
	}

	return path
}

// GetConfig returns complete configuration
func (inv *Inventory) GetConfig() *Config {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return inv.config
}
