// Package ssh provides SSH client functionality for remote command execution
// and file operations.
//
// The Client struct encapsulates SSH connection management and supports:
//   - Key-based and password authentication
//   - ssh-agent integration
//   - ~/.ssh/config file parsing
//   - Command execution with output capture
//   - File upload (copy) and download (fetch)
//
// Host Specification
//
// Hosts are specified using HostSpec, which defines connection parameters:
//   - Address: The host address (hostname or IP)
//   - User: SSH username (defaults from config or "root")
//   - Port: SSH port (defaults to 22)
//   - KeyPath: Path to private key file
//
// Configuration Priority (highest to lowest):
//   1. Explicitly set values (UserSet, PortSet, KeyPathSet)
//   2. Host-specific overrides in TOML config
//   3. Group-level overrides in TOML config
//   4. ~/.ssh/config file entries
//   5. Default values
//
// Example Usage:
//
//	client, err := ssh.NewClient("~/.ssh/id_rsa")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	spec := ssh.HostSpec{
//	    Address: "example.com",
//	    User:    "ubuntu",
//	    Port:    22,
//	}
//
//	result, err := client.Exec(spec, "ls -la", "", 30*time.Second)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(string(result.Output))
package ssh

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Client represents an SSH client with configuration for remote connections.
// Client is safe for concurrent use.
type Client struct {
	// config contains the SSH client configuration including authentication methods
	// and host key verification callback.
	config *ssh.ClientConfig
	// keyPath stores the path to the private key file.
	keyPath string
}

// HostSpec defines the parameters for connecting to a remote host.
//
// The boolean fields (UserSet, PortSet, KeyPathSet) indicate whether the
// corresponding values were explicitly set by the user, which affects
// configuration priority when merging with defaults and SSH config files.
type HostSpec struct {
	// Address is the hostname or IP address of the remote host.
	Address string
	// User is the SSH username for authentication.
	User string
	// Port is the SSH port number (typically 22).
	Port int
	// KeyPath is the path to the private key file for authentication.
	KeyPath string
	// UserSet indicates if User was explicitly configured.
	UserSet bool
	// PortSet indicates if Port was explicitly configured.
	PortSet bool
	// KeyPathSet indicates if KeyPath was explicitly configured.
	KeyPathSet bool
}

// ClientOption configures a Client during creation.
type ClientOption func(*clientConfig)

type clientConfig struct {
	knownHostsPath string
	strictHostKey  bool
}

// WithKnownHosts sets the path to the known_hosts file.
func WithKnownHosts(path string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.knownHostsPath = path
	}
}

// WithStrictHostKey enables strict host key checking.
// When true, connections to unknown hosts will be rejected.
// When false (default), unknown host keys will be automatically added.
func WithStrictHostKey(strict bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.strictHostKey = strict
	}
}

// NewClient creates a new SSH client.
// By default, it uses AutoAdd mode for host key verification (unknown hosts are accepted and added to known_hosts).
// Use WithKnownHosts and WithStrictHostKey options to customize this behavior.
func NewClient(keyPath string, opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		knownHostsPath: "", // Will use default
		strictHostKey:  false,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Expand ~ in keyPath
	expandedKeyPath := keyPath
	if len(keyPath) > 0 && keyPath[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(keyPath) > 1 && keyPath[1] == '/' {
			expandedKeyPath = filepath.Join(home, keyPath[2:])
		} else {
			expandedKeyPath = home
		}
	}

	// Build authentication methods with fallback support
	authMethods := buildAuthMethods(expandedKeyPath)

	// Build host key callback
	hostKeyCallback, err := buildHostKeyCallback(cfg.knownHostsPath, cfg.strictHostKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create host key callback: %w", err)
	}

	return &Client{
		config: &ssh.ClientConfig{
			User:            "root",
			Auth:            authMethods,
			HostKeyCallback: hostKeyCallback,
			Timeout:         30 * time.Second,
		},
		keyPath: expandedKeyPath,
	}, nil
}

// buildHostKeyCallback creates the host key callback based on configuration.
func buildHostKeyCallback(knownHostsPath string, strictHostKey bool) (ssh.HostKeyCallback, error) {
	// autoAdd is true when strict mode is disabled
	autoAdd := !strictHostKey
	verifier, err := NewKnownHostsVerifier(knownHostsPath, autoAdd)
	if err != nil {
		// If we can't create verifier, fall back to insecure callback for compatibility
		// This should not happen in normal operation
		return ssh.InsecureIgnoreHostKey(), nil
	}
	return verifier.HostKeyCallback(), nil
}

// buildAuthMethods builds authentication method list with fallback support
func buildAuthMethods(keyPath string) []ssh.AuthMethod {
	methods := []ssh.AuthMethod{}

	// 1. Try ssh-agent first (if available)
	if agentSigner, err := getAgentSigner(); err == nil {
		methods = append(methods, ssh.PublicKeys(agentSigner))
	}

	// 2. Try specified key file
	if keyPath != "" {
		if keySigner, err := parsePrivateKey(keyPath); err == nil {
			methods = append(methods, ssh.PublicKeys(keySigner))
		}
	}

	// 3. If no key specified, try default key locations
	if keyPath == "" {
		defaultKeys := []string{
			"id_rsa",
			"id_ed25519",
			"id_ecdsa",
			"id_ecdsa_sk",
			"id_ed25519_sk",
		}
		home, _ := os.UserHomeDir()
		for _, key := range defaultKeys {
			fullPath := filepath.Join(home, ".ssh", key)
			if keySigner, err := parsePrivateKey(fullPath); err == nil {
				methods = append(methods, ssh.PublicKeys(keySigner))
			}
		}
	}

	return methods
}

// parsePrivateKey parses private key file
func parsePrivateKey(keyPath string) (ssh.Signer, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(key)
}

// NewClientWithPassword creates a client using password authentication.
// By default, it uses AutoAdd mode for host key verification.
func NewClientWithPassword(user, password string, opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		knownHostsPath: "", // Will use default
		strictHostKey:  false,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Build host key callback
	hostKeyCallback, err := buildHostKeyCallback(cfg.knownHostsPath, cfg.strictHostKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create host key callback: %w", err)
	}

	return &Client{
		config: &ssh.ClientConfig{
			User:            user,
			Auth:            []ssh.AuthMethod{ssh.Password(password)},
			HostKeyCallback: hostKeyCallback,
			Timeout:         30 * time.Second,
		},
	}, nil
}

// hostsCache caches /etc/hosts resolution results
var hostsCache = make(map[string]string)
var hostsCacheLoaded bool

// loadHostsFile loads /etc/hosts file into cache
func loadHostsFile() map[string]string {
	m := make(map[string]string)

	f, err := os.Open(hostsFilePath)
	if err != nil {
		return m
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过注释和空行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		ip := fields[0]
		// Skip IPv6 and invalid IPs
		if strings.Contains(ip, ":") {
			continue
		}
		if net.ParseIP(ip) == nil {
			continue
		}

		// First field is IP, followed by hostnames
		for _, hostname := range fields[1:] {
			// Skip comments
			if strings.HasPrefix(hostname, "#") {
				break
			}
			hostname = strings.TrimSuffix(hostname, "\n")
			if hostname != "" {
				m[hostname] = ip
			}
		}
	}

	return m
}

// lookupHostsFile looks up hostname in /etc/hosts
func lookupHostsFile(hostname string) (string, bool) {
	if !hostsCacheLoaded {
		hostsCache = loadHostsFile()
		hostsCacheLoaded = true
	}

	ip, found := hostsCache[hostname]
	return ip, found
}

// resolveHost resolves host address, tries DNS first, falls back to /etc/hosts
func resolveHost(host string) (string, error) {
	// If already an IP address, return directly
	if ip := net.ParseIP(host); ip != nil {
		return host, nil
	}

	// Try normal DNS resolution first
	addrs, err := net.LookupHost(host)
	if err == nil && len(addrs) > 0 {
		// Return first IPv4 address
		for _, addr := range addrs {
			if !strings.Contains(addr, ":") {
				return addr, nil
			}
		}
		// If only IPv6, return first
		return addrs[0], nil
	}

	// DNS resolution failed, try /etc/hosts
	if ip, found := lookupHostsFile(host); found {
		return ip, nil
	}

	return "", fmt.Errorf("host not found: %s", host)
}

// SSHConfigEntry represents a Host entry in SSH config file
type SSHConfigEntry struct {
	Patterns   []string
	HostName   string
	User       string
	Port       int
	KeyPath    string
	ProxyJump  string
	ProxyCommand string
}

// sshConfigCache caches SSH config
var sshConfigCache []SSHConfigEntry
var sshConfigLoaded bool

// expandPath expands ~ and environment variables
func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		if len(path) > 1 && path[1] == '/' {
			path = filepath.Join(home, path[2:])
		} else {
			path = home
		}
	}
	return os.ExpandEnv(path)
}

// loadSSHConfig loads SSH config file
func loadSSHConfig() []SSHConfigEntry {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".ssh", "config")
	entries := []SSHConfigEntry{}

	f, err := os.Open(configPath)
	if err != nil {
		return entries
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var current *SSHConfigEntry

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 跳过注释和空行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check if it's a Host directive
		if strings.HasPrefix(line, "Host ") {
			// Save previous entry
			if current != nil {
				entries = append(entries, *current)
			}
			// Create new entry
			patterns := strings.Fields(line[5:])
			current = &SSHConfigEntry{
				Patterns: patterns,
			}
			continue
		}

		// Parse other directives
		if current == nil {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		key := strings.ToLower(parts[0])
		value := strings.Join(parts[1:], " ")

		switch key {
		case "hostname":
			current.HostName = value
		case "user":
			current.User = value
		case "port":
			if port, err := parsePort(value); err == nil {
				current.Port = port
			}
		case "identityfile":
			current.KeyPath = expandPath(value)
		case "proxyjump":
			current.ProxyJump = value
		case "proxycommand":
			current.ProxyCommand = value
		}
	}

	// Save last entry
	if current != nil {
		entries = append(entries, *current)
	}

	return entries
}

// parsePort parses port number
func parsePort(s string) (int, error) {
	var port int
	_, err := fmt.Sscanf(s, "%d", &port)
	return port, err
}

// matchHostPattern checks if hostname matches pattern
func matchHostPattern(host, pattern string) bool {
	// Exact match
	if host == pattern {
		return true
	}

	// Wildcard matching
	if strings.Contains(pattern, "*") || strings.Contains(pattern, "?") {
		// Simple wildcard matching
		patternRegex := strings.ReplaceAll(pattern, ".", "\\.")
		patternRegex = strings.ReplaceAll(patternRegex, "*", ".*")
		patternRegex = strings.ReplaceAll(patternRegex, "?", ".")
		patternRegex = "^" + patternRegex + "$"
		matched, _ := path.Match(pattern, host)
		return matched
	}

	// Negative match (!pattern)
	if strings.HasPrefix(pattern, "!") {
		return !matchHostPattern(host, pattern[1:])
	}

	return false
}

// findSSHConfigEntry finds SSH config entry matching host
func findSSHConfigEntry(host string) SSHConfigEntry {
	if !sshConfigLoaded {
		sshConfigCache = loadSSHConfig()
		sshConfigLoaded = true
	}

	// Find matching entries (later ones override earlier ones)
	var result SSHConfigEntry
	for _, entry := range sshConfigCache {
		for _, pattern := range entry.Patterns {
			if matchHostPattern(host, pattern) {
				// Merge configuration
				if entry.HostName != "" {
					result.HostName = entry.HostName
				}
				if entry.User != "" {
					result.User = entry.User
				}
				if entry.Port != 0 {
					result.Port = entry.Port
				}
				if entry.KeyPath != "" {
					result.KeyPath = entry.KeyPath
				}
				if entry.ProxyJump != "" {
					result.ProxyJump = entry.ProxyJump
				}
				if entry.ProxyCommand != "" {
					result.ProxyCommand = entry.ProxyCommand
				}
				break
			}
		}
	}
	return result
}

// applySSHConfig applies SSH config to HostSpec
// Priority: TOML config > ~/.ssh/config > defaults
func applySSHConfig(spec HostSpec) HostSpec {
	entry := findSSHConfigEntry(spec.Address)

	result := spec

	// HostName in SSH config replaces original address (alias resolution)
	// But if TOML already has an IP address, don't replace
	if entry.HostName != "" && !isIP(result.Address) {
		result.Address = entry.HostName
	}

	// Only use SSH config values if not explicitly set in TOML
	if !result.UserSet && result.User == "" {
		result.User = entry.User
	}
	if !result.PortSet && result.Port == 0 && entry.Port != 0 {
		result.Port = entry.Port
	}
	if !result.KeyPathSet && result.KeyPath == "" && entry.KeyPath != "" {
		result.KeyPath = entry.KeyPath
	}

	return result
}

// isIP checks if string is an IP address
func isIP(s string) bool {
	return net.ParseIP(s) != nil
}

// Connect connects to specified host
func (c *Client) Connect(spec HostSpec) (*ssh.Client, error) {
	// First apply ~/.ssh/config configuration
	spec = applySSHConfig(spec)

	config := *c.config // Copy config
	config.User = spec.User

	if spec.Port == 0 {
		spec.Port = 22
	}

	// Resolve host address: try normal resolution first, fall back to /etc/hosts
	hostAddr, err := resolveHost(spec.Address)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve host %s: %w", spec.Address, err)
	}

	addr := fmt.Sprintf("%s:%d", hostAddr, spec.Port)
	client, err := ssh.Dial("tcp", addr, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to dial %s: %w", addr, err)
	}

	return client, nil
}

// ExecResult contains the result of executing a command on a remote host.
type ExecResult struct {
	// Host is the address of the host where the command was executed.
	Host string
	// Output contains the standard output from the command.
	Output []byte
	// Error contains the standard error output from the command.
	Error []byte
	// ExitCode is the exit status returned by the command.
	ExitCode int
	// ErrorMsg contains any error that occurred during execution.
	ErrorMsg error
}

// Exec executes command on remote host
func (c *Client) Exec(spec HostSpec, cmd string, input string, timeout time.Duration) (*ExecResult, error) {
	client, err := c.Connect(spec)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Set input if provided
	if input != "" {
		session.Stdin = strings.NewReader(input)
	}

	// Set output buffers first, then execute command
	var stdoutBuf, stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// Set timeout
	if timeout > 0 {
		type result struct {
			err error
		}
		done := make(chan result, 1)
		go func() {
			done <- result{session.Run(cmd)}
		}()

		select {
		case res := <-done:
			return c.parseResult(spec.Address, &stdoutBuf, &stderrBuf, res.err)
		case <-time.After(timeout):
			// Send SIGINT to the process before closing session
			_ = session.Signal(ssh.SIGINT)
			// Close the session to terminate the command
			session.Close()
			// Wait a bit for the goroutine to finish and capture any remaining output
			select {
			case res := <-done:
				// Goroutine finished, return partial result
				return c.parseResult(spec.Address, &stdoutBuf, &stderrBuf,
					fmt.Errorf("command timed out after %v: %w", timeout, res.err))
			case <-time.After(100 * time.Millisecond):
				// Goroutine didn't finish, return timeout with captured output
				return c.parseResult(spec.Address, &stdoutBuf, &stderrBuf,
					fmt.Errorf("command timed out after %v", timeout))
			}
		}
	}

	err = session.Run(cmd)
	return c.parseResult(spec.Address, &stdoutBuf, &stderrBuf, err)
}

// ExecStream executes command and streams output
func (c *Client) ExecStream(spec HostSpec, cmd string, input string, timeout time.Duration) (stdout, stderr io.ReadCloser, err error) {
	client, err := c.Connect(spec)
	if err != nil {
		return nil, nil, err
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Set input if provided
	if input != "" {
		session.Stdin = strings.NewReader(input)
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, nil, err
	}

	stderrPipe, err := session.StderrPipe()
	if err != nil {
		session.Close()
		client.Close()
		return nil, nil, err
	}

	if err := session.Start(cmd); err != nil {
		session.Close()
		client.Close()
		return nil, nil, err
	}

	// Return reader with cleanup
	return &readCloser{Reader: stdoutPipe, closeFunc: func() error {
		session.Wait()
		session.Close()
		client.Close()
		return nil
	}}, &readCloser{Reader: stderrPipe, closeFunc: func() error {
		return nil
	}}, nil
}

// parseResult parses command execution result
func (c *Client) parseResult(host string, stdoutBuf, stderrBuf *bytes.Buffer, err error) (*ExecResult, error) {
	result := &ExecResult{Host: host}

	result.Output = stdoutBuf.Bytes()
	result.Error = stderrBuf.Bytes()

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		}
		result.ErrorMsg = err
	}

	return result, nil
}

// Copy copies file to remote host
func (c *Client) Copy(spec HostSpec, src, dest string, mode os.FileMode) error {
	client, err := c.Connect(spec)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Read source file
	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}

	// Use scp protocol
	go func() {
		w, _ := session.StdinPipe()
		defer w.Close()

		fmt.Fprintf(w, "C%04o %d %s\n", mode, len(content), filepath.Base(dest))
		w.Write(content)
		fmt.Fprint(w, "\x00")
	}()

	// Execute scp -t command
	destDir := filepath.Dir(dest)
	if destDir == "" {
		destDir = "."
	}
	cmd := fmt.Sprintf("scp -t %s", destDir)

	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("scp failed: %w", err)
	}

	return nil
}

// Fetch downloads file from remote host
func (c *Client) Fetch(spec HostSpec, src, dest string) error {
	client, err := c.Connect(spec)
	if err != nil {
		return err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Prepare to receive file content
	var content bytes.Buffer
	session.Stdout = &content

	// Use cat command to read file (simple and reliable)
	if err := session.Run(fmt.Sprintf("cat %s", src)); err != nil {
		return fmt.Errorf("failed to fetch file: %w", err)
	}

	// Write to local file
	if err := os.WriteFile(dest, content.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// TestConnection tests connection
func (c *Client) TestConnection(spec HostSpec) error {
	client, err := c.Connect(spec)
	if err != nil {
		return err
	}
	client.Close()
	return nil
}

// readCloser wraps io.Reader to add close functionality
type readCloser struct {
	io.Reader
	closeFunc func() error
}

func (rc *readCloser) Close() error {
	if rc.closeFunc != nil {
		return rc.closeFunc()
	}
	return nil
}

// getAgentSigner tries to get signer from ssh-agent
func getAgentSigner() (ssh.Signer, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	conn, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to ssh-agent: %w", err)
	}

	agentClient := agent.NewClient(conn)
	signers, err := agentClient.Signers()
	if err != nil {
		return nil, fmt.Errorf("failed to get signers from agent: %w", err)
	}

	if len(signers) == 0 {
		return nil, fmt.Errorf("no signers available in ssh-agent")
	}

	return signers[0], nil
}
