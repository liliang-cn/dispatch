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

// ExpandWildcardFromSSHConfig expands a wildcard pattern against SSH config entries
// Returns a list of host aliases that match the pattern
func ExpandWildcardFromSSHConfig(pattern string) []string {
	entries, err := LoadSSHConfig()
	if err != nil {
		return nil
	}

	var matches []string
	seen := make(map[string]bool)

	// Convert pattern to a simple glob (only * at the end for now)
	for _, e := range entries {
		for _, hostPattern := range e.HostPatterns {
			// Skip if this host is already matched
			if seen[hostPattern] {
				continue
			}

			if matchPattern(pattern, hostPattern) {
				matches = append(matches, hostPattern)
				seen[hostPattern] = true
			}
		}
	}

	return matches
}

// matchPattern matches a pattern against a string
// Supports * wildcard only
func matchPattern(pattern, s string) bool {
	// Fast path: exact match
	if pattern == s {
		return true
	}

	// Simple wildcard matching (only * at the end)
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}

	// If pattern starts with *, match suffix
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(s, suffix)
	}

	// If pattern has * in middle, split and match both sides
	if idx := strings.Index(pattern, "*"); idx >= 0 {
		prefix := pattern[:idx]
		suffix := pattern[idx+1:]
		return strings.HasPrefix(s, prefix) && strings.HasSuffix(s, suffix)
	}

	return false
}

// ReloadSSHConfig clears the cache and reloads SSH config
func ReloadSSHConfig() {
	globalSSHConfig.mu.Lock()
	defer globalSSHConfig.mu.Unlock()
	globalSSHConfig.loaded = false
	globalSSHConfig.entries = nil
}

// GetAllSSHConfigHosts returns all host entries from SSH config
// Returns a map of host pattern -> SSHConfigEntry
func GetAllSSHConfigHosts() map[string]SSHConfigEntry {
	entries, err := LoadSSHConfig()
	if err != nil {
		return nil
	}

	result := make(map[string]SSHConfigEntry)
	for _, e := range entries {
		for _, pattern := range e.HostPatterns {
			// Skip wildcard patterns and "Host *" entries
			if pattern == "*" || strings.Contains(pattern, "*") {
				continue
			}
			result[pattern] = e
		}
	}
	return result
}
