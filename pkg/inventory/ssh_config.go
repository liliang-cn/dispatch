package inventory

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// SSHConfigEntry represents a parsed SSH config entry
type SSHConfigEntry struct {
	HostPatterns []string // Host patterns (e.g., ["gui01", "gui*"])
	HostName     string
	User         string
	Port         int
	KeyPath      string
}

// sshConfigCache caches parsed SSH config
type sshConfigCache struct {
	entries []SSHConfigEntry
	mu      sync.RWMutex
	loaded  bool
}

var globalSSHConfig = &sshConfigCache{}

// LoadSSHConfig loads and parses ~/.ssh/config
func LoadSSHConfig() ([]SSHConfigEntry, error) {
	globalSSHConfig.mu.Lock()
	defer globalSSHConfig.mu.Unlock()

	if globalSSHConfig.loaded {
		return globalSSHConfig.entries, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home dir: %w", err)
	}

	configPath := filepath.Join(home, ".ssh", "config")
	f, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			globalSSHConfig.loaded = true
			globalSSHConfig.entries = []SSHConfigEntry{}
			return globalSSHConfig.entries, nil
		}
		return nil, fmt.Errorf("failed to open ssh config: %w", err)
	}
	defer f.Close()

	entries, err := parseSSHConfig(f)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ssh config: %w", err)
	}

	globalSSHConfig.entries = entries
	globalSSHConfig.loaded = true

	return entries, nil
}

// parseSSHConfig parses SSH config file
func parseSSHConfig(f *os.File) ([]SSHConfigEntry, error) {
	scanner := bufio.NewScanner(f)
	var entries []SSHConfigEntry
	var currentEntry *SSHConfigEntry

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Split line into keyword and arguments
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		keyword := strings.ToLower(fields[0])

		switch keyword {
		case "host":
			// Save previous entry
			if currentEntry != nil && len(currentEntry.HostPatterns) > 0 {
				entries = append(entries, *currentEntry)
			}
			// Start new entry
			currentEntry = &SSHConfigEntry{
				HostPatterns: fields[1:],
			}
		case "hostname":
			if currentEntry != nil && len(fields) > 1 {
				currentEntry.HostName = fields[1]
			}
		case "user":
			if currentEntry != nil && len(fields) > 1 {
				currentEntry.User = fields[1]
			}
		case "port":
			if currentEntry != nil && len(fields) > 1 {
				if port, err := strconv.Atoi(fields[1]); err == nil {
					currentEntry.Port = port
				}
			}
		case "identityfile":
			if currentEntry != nil && len(fields) > 1 {
				currentEntry.KeyPath = fields[1]
			}
		}
	}

	// Save last entry
	if currentEntry != nil && len(currentEntry.HostPatterns) > 0 {
		entries = append(entries, *currentEntry)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return entries, nil
}

// GetSSHConfigEntry looks up a host in SSH config by alias or address
// Returns the first matching entry
func GetSSHConfigEntry(hostOrAlias string) (SSHConfigEntry, bool) {
	entries, err := LoadSSHConfig()
	if err != nil {
		return SSHConfigEntry{}, false
	}

	for _, e := range entries {
		for _, pattern := range e.HostPatterns {
			// Exact match
			if pattern == hostOrAlias {
				return e, true
			}
			// Simple wildcard support (only * at the end)
			if strings.HasSuffix(pattern, "*") {
				prefix := strings.TrimSuffix(pattern, "*")
				if strings.HasPrefix(hostOrAlias, prefix) {
					return e, true
				}
			}
			// Check if HostName matches
			if e.HostName == hostOrAlias {
				return e, true
			}
		}
	}

	return SSHConfigEntry{}, false
}

// ReloadSSHConfig clears the cache and reloads SSH config
func ReloadSSHConfig() {
	globalSSHConfig.mu.Lock()
	defer globalSSHConfig.mu.Unlock()
	globalSSHConfig.loaded = false
	globalSSHConfig.entries = nil
}
