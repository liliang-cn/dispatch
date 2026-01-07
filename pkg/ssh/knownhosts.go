package ssh

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

var (
	// ErrHostKeyUnknown is returned when the host key is not in known_hosts.
	ErrHostKeyUnknown = errors.New("host key unknown")
	// ErrHostKeyChanged is returned when the host key differs from known_hosts.
	ErrHostKeyChanged = errors.New("host key changed")
)

// KnownHostsVerifier handles host key verification using known_hosts file.
type KnownHostsVerifier struct {
	knownHostsPath string
	hostKeys       map[string][]ssh.PublicKey // Map of host patterns to keys
	autoAdd        bool                        // Automatically add unknown host keys
	mu             sync.RWMutex
}

// NewKnownHostsVerifier creates a verifier from known_hosts file.
//
// If autoAdd is true, unknown host keys will be automatically added to known_hosts.
// If autoAdd is false, connections to unknown hosts will be rejected.
func NewKnownHostsVerifier(path string, autoAdd bool) (*KnownHostsVerifier, error) {
	v := &KnownHostsVerifier{
		knownHostsPath: expandKnownHostsPath(path),
		hostKeys:       make(map[string][]ssh.PublicKey),
		autoAdd:        autoAdd,
	}

	if err := v.load(); err != nil {
		return nil, fmt.Errorf("failed to load known_hosts: %w", err)
	}

	return v, nil
}

// load parses the known_hosts file.
func (v *KnownHostsVerifier) load() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	f, err := os.Open(v.knownHostsPath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet, create directory and return empty
			if err := os.MkdirAll(filepath.Dir(v.knownHostsPath), 0755); err != nil {
				return fmt.Errorf("failed to create known_hosts directory: %w", err)
			}
			return nil
		}
		return err
	}
	defer f.Close()

	v.hostKeys = make(map[string][]ssh.PublicKey)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, err := parseKnownHostsLine(line)
		if err != nil {
			// Skip invalid lines
			continue
		}

		for _, pattern := range entry.patterns {
			v.hostKeys[pattern] = append(v.hostKeys[pattern], entry.publicKey)
		}
	}

	return scanner.Err()
}

// knownHostsEntry represents a parsed known_hosts entry.
type knownHostsEntry struct {
	patterns  []string
	publicKey ssh.PublicKey
}

// parseKnownHostsLine parses a single line from known_hosts.
// Supports formats:
//   - host key-type key-data
//   - [host1,host2,...] key-type key-data
func parseKnownHostsLine(line string) (*knownHostsEntry, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil, fmt.Errorf("invalid line format")
	}

	var patterns []string
	var keyData string

	// Check for bracketed host format [host1,host2,...]
	if strings.HasPrefix(fields[0], "[") {
		bracketEnd := strings.Index(fields[0], "]")
		if bracketEnd == -1 {
			return nil, fmt.Errorf("invalid bracket format")
		}
		patternList := fields[0][1:bracketEnd]
		patterns = strings.Split(patternList, ",")
		keyData = strings.Join(fields[1:], " ")
	} else {
		// Simple format: host key-type key-data
		patterns = []string{fields[0]}
		keyData = strings.Join(fields[1:], " ")
	}

	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(keyData))
	if err != nil {
		return nil, fmt.Errorf("failed to parse key: %w", err)
	}

	return &knownHostsEntry{
		patterns:  patterns,
		publicKey: publicKey,
	}, nil
}

// Verify checks if the host key matches known_hosts.
func (v *KnownHostsVerifier) Verify(hostname string, remote net.Addr, key ssh.PublicKey) error {
	v.mu.RLock()

	// Normalize hostname
	host, _, err := net.SplitHostPort(remote.String())
	if err != nil {
		host = remote.String()
	}

	// Check for matching known key
	matched, hasMatch, err := v.findMatchingKey(host, key)
	v.mu.RUnlock()

	if err != nil {
		return err
	}

	if hasMatch && matched {
		return nil // Key matches known_hosts
	}

	if hasMatch && !matched {
		return fmt.Errorf("%w: %s", ErrHostKeyChanged, host)
	}

	// Key not found
	if !v.autoAdd {
		return fmt.Errorf("%w: %s", ErrHostKeyUnknown, host)
	}

	// Auto-add mode: add the key to known_hosts
	// Note: We must release RLock before calling Add which needs Lock
	return v.Add(host, key)
}

// findMatchingKey searches for a matching key in known_hosts.
// Returns (matched, hasMatch, error).
// matched=true if key matches, hasMatch=true if host has any entry.
func (v *KnownHostsVerifier) findMatchingKey(host string, key ssh.PublicKey) (bool, bool, error) {
	keyBytes := key.Marshal()

	for pattern, keys := range v.hostKeys {
		if matchHostPattern(host, pattern) {
			if len(keys) == 0 {
				return false, true, nil
			}
			for _, knownKey := range keys {
				if bytes.Equal(knownKey.Marshal(), keyBytes) {
					return true, true, nil // Key matches
				}
			}
			// Same pattern but different key - host key changed!
			return false, true, nil
		}
	}

	return false, false, nil // No match found
}

// Add adds a new host key to known_hosts.
func (v *KnownHostsVerifier) Add(host string, key ssh.PublicKey) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Append to file
	f, err := os.OpenFile(v.knownHostsPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open known_hosts: %w", err)
	}
	defer f.Close()

	keyData := ssh.MarshalAuthorizedKey(key)
	line := fmt.Sprintf("%s %s", host, strings.TrimSpace(string(keyData)))

	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("failed to write to known_hosts: %w", err)
	}

	// Update in-memory cache
	v.hostKeys[host] = append(v.hostKeys[host], key)

	return nil
}

// HostKeyCallback returns an ssh.HostKeyCallback for use with ssh.ClientConfig.
func (v *KnownHostsVerifier) HostKeyCallback() ssh.HostKeyCallback {
	return ssh.HostKeyCallback(v.Verify)
}

// expandKnownHostsPath expands ~ in path.
func expandKnownHostsPath(path string) string {
	if path == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".ssh", "known_hosts")
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[2:])
	}
	return path
}
