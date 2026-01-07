// Package executor provides parallel execution of SSH operations across multiple hosts.
//
// The Executor type manages concurrent SSH connections and coordinates execution
// across hosts with configurable parallelism and timeout handling.
//
// Callback Pattern
//
// Results are delivered via callback functions as they complete, enabling
// real-time processing without waiting for all hosts to finish.
//
// Example Usage:
//
//	executor := executor.NewExecutor(inv)
//
//	req := &executor.ExecRequest{
//	    Hosts:    []string{"web", "db"},
//	    Cmd:      "uptime",
//	    Parallel: 10,
//	    Timeout:  30 * time.Second,
//	}
//
//	err := executor.Exec(ctx, req, func(result *executor.ExecResult) {
//	    fmt.Printf("[%s] %s\n", result.Host, result.Output)
//	})
package executor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/logger"
	dispatchssh "github.com/liliang-cn/dispatch/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// SSHClient interface abstracts the SSH client interactions
type SSHClient interface {
	Connect(spec dispatchssh.HostSpec) (*ssh.Client, error)
	Exec(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (*dispatchssh.ExecResult, error)
	ExecStream(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (io.ReadCloser, io.ReadCloser, error)
	Copy(spec dispatchssh.HostSpec, src, dest string, mode os.FileMode) error
	Fetch(spec dispatchssh.HostSpec, src, dest string) error
}

// Executor handles parallel execution
type Executor struct {
	inv        *inventory.Inventory
	logger     *logger.Logger
	connCache  map[string]*clientConn // Cache for connections within a single operation
	connMu     sync.Mutex             // Protects connCache
	baseClient SSHClient              // Base SSH client for creating connections
}

// clientConn wraps an SSH client with metadata for connection reuse.
type clientConn struct {
	client   *ssh.Client
	refCount int
}

// NewExecutor creates a new executor
func NewExecutor(inv *inventory.Inventory) *Executor {
	cfg := inv.GetConfig()
	logCfg := &logger.Config{
		Level:    cfg.Log.Level,
		Output:   cfg.Log.Output,
		NoColor:  cfg.Log.NoColor,
		ShowTime: cfg.Log.ShowTime,
	}
	return &Executor{
		inv:    inv,
		logger: logger.New(logCfg),
	}
}

// SetLogger sets custom logger
func (e *Executor) SetLogger(l *logger.Logger) {
	e.logger = l
}

// GetLogger gets logger
func (e *Executor) GetLogger() *logger.Logger {
	return e.logger
}

// getSSHClientOptions returns SSH client options based on inventory config.
func (e *Executor) getSSHClientOptions() []dispatchssh.ClientOption {
	cfg := e.inv.GetConfig().SSH
	opts := []dispatchssh.ClientOption{}
	if cfg.KnownHostsPath != "" {
		opts = append(opts, dispatchssh.WithKnownHosts(cfg.KnownHostsPath))
	}
	if cfg.StrictHostKey {
		opts = append(opts, dispatchssh.WithStrictHostKey(true))
	}
	return opts
}

// SetBaseClient sets the base SSH client (useful for testing)
func (e *Executor) SetBaseClient(client SSHClient) {
	e.baseClient = client
}

// beginOperation initializes the connection cache for a new operation.
func (e *Executor) beginOperation() error {
	e.connMu.Lock()
	defer e.connMu.Unlock()

	e.connCache = make(map[string]*clientConn)

	// Create base SSH client if not already set
	if e.baseClient == nil {
		client, err := dispatchssh.NewClient(e.inv.GetConfig().SSH.KeyPath, e.getSSHClientOptions()...)
		if err != nil {
			return fmt.Errorf("failed to create SSH client: %w", err)
		}
		e.baseClient = client
	}
	return nil
}

// endOperation closes all cached connections and cleans up.
func (e *Executor) endOperation() {
	e.connMu.Lock()
	defer e.connMu.Unlock()

	for _, conn := range e.connCache {
		conn.client.Close()
	}
	e.connCache = nil
	e.baseClient = nil
}

// getConnection gets or creates a connection for the given host spec.
// Connections are reused within a single operation.
func (e *Executor) getConnection(spec dispatchssh.HostSpec) (*ssh.Client, error) {
	// Create a unique key for this host
	key := fmt.Sprintf("%s@%s:%d", spec.User, spec.Address, spec.Port)

	e.connMu.Lock()
	defer e.connMu.Unlock()

	// Check if we have a cached connection
	if conn, ok := e.connCache[key]; ok {
		conn.refCount++
		return conn.client, nil
	}

	// Create new connection
	client, err := e.baseClient.Connect(spec)
	if err != nil {
		return nil, err
	}

	// Cache the connection
	e.connCache[key] = &clientConn{
		client:   client,
		refCount: 1,
	}

	return client, nil
}

// execOnConn executes a command on an existing SSH connection.
func (e *Executor) execOnConn(sshClient *ssh.Client, cmd string, input string, timeout time.Duration) (*dispatchssh.ExecResult, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Set input if provided
	if input != "" {
		session.Stdin = strings.NewReader(input)
	}

	// Set output buffers
	var stdoutBuf, stderrBuf strings.Builder
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf

	// Set timeout
	if timeout > 0 {
		done := make(chan error, 1)
		go func() {
			done <- session.Run(cmd)
		}()

		select {
		case err := <-done:
			return e.parseExecResult(stdoutBuf.String(), stderrBuf.String(), err)
		case <-time.After(timeout):
			sshClient.Close()
			return nil, fmt.Errorf("command timed out after %v", timeout)
		}
	}

	err = session.Run(cmd)
	return e.parseExecResult(stdoutBuf.String(), stderrBuf.String(), err)
}

// parseExecResult parses the result of command execution.
func (e *Executor) parseExecResult(stdout, stderr string, err error) (*dispatchssh.ExecResult, error) {
	result := &dispatchssh.ExecResult{
		Output: []byte(stdout),
		Error:  []byte(stderr),
	}

	if err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			result.ExitCode = exitErr.ExitStatus()
		}
		result.ErrorMsg = err
	}

	return result, nil
}

// copyFile copies a file to the remote host using an existing SSH connection.
func (e *Executor) copyFile(sshClient *ssh.Client, src, dest string, mode os.FileMode) error {
	session, err := sshClient.NewSession()
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

// fetchFile downloads a file from the remote host using an existing SSH connection.
func (e *Executor) fetchFile(sshClient *ssh.Client, src, dest string) (int64, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return 0, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Prepare to receive file content
	var content strings.Builder
	session.Stdout = &content

	// Use cat command to read file (simple and reliable)
	if err := session.Run(fmt.Sprintf("cat %s", src)); err != nil {
		return 0, fmt.Errorf("failed to fetch file: %w", err)
	}

	// Write to local file
	data := content.String()
	if err := os.WriteFile(dest, []byte(data), 0644); err != nil {
		return 0, fmt.Errorf("failed to write file: %w", err)
	}

	return int64(len(data)), nil
}

// ExecRequest command execution request
type ExecRequest struct {
	Hosts          []string
	Cmd            string
	Input          string // Standard input to pass to the command
	Env            map[string]string
	Dir            string
	Parallel       int
	Timeout        time.Duration
	Stream         bool                                            // Whether to use streaming output
	StreamCallback func(host, streamType string, data []byte) // Callback for streaming output
}

// ExecResult execution result
type ExecResult struct {
	Host      string
	Output    []byte
	Error     []byte
	ExitCode  int
	StartTime time.Time
	EndTime   time.Time
	Err       error
}

// ExecResultCallback callback function to receive execution results
type ExecResultCallback func(result *ExecResult)

// Exec executes command (with callback)
func (e *Executor) Exec(ctx context.Context, req *ExecRequest, callback ExecResultCallback) error {
	// If streaming output is requested, use streaming mode
	if req.Stream {
		return e.ExecStreamWithCallback(ctx, req, callback)
	}

	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		e.logger.Error("failed to get hosts: %v", err)
		return err
	}

	e.logger.Debug("executing on %d hosts: %v", len(hosts), req.Hosts)

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	if req.Timeout <= 0 {
		req.Timeout = 300 * time.Second
	}

	// Initialize connection cache for this operation
	if err := e.beginOperation(); err != nil {
		return err
	}
	defer e.endOperation()

	// 并发控制
	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup
	var successCount, failCount int32

	for _, host := range hosts {
		e.logger.Debug("executing on host %s", host.Address)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore

			result := &ExecResult{
				Host:      h.Address,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			cmd := e.buildCommand(req.Cmd, req.Env, req.Dir)

			// Get or create connection for this host
			conn, err := e.getConnection(spec)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				atomic.AddInt32(&failCount, 1)
				callback(result)
				return
			}

			execResult, err := e.execOnConn(conn, cmd, req.Input, req.Timeout)
			result.EndTime = time.Now()

			if err != nil {
				result.Err = err
				atomic.AddInt32(&failCount, 1)
			} else {
				result.Output = execResult.Output
				result.Error = execResult.Error
				result.ExitCode = execResult.ExitCode
				if execResult.ExitCode == 0 {
					atomic.AddInt32(&successCount, 1)
				} else {
					atomic.AddInt32(&failCount, 1)
				}
			}

			callback(result)
		}(host)
	}

	wg.Wait()
	return nil
}

// ExecStreamWithCallback executes command with streaming output, calls callback at the end
func (e *Executor) ExecStreamWithCallback(ctx context.Context, req *ExecRequest, callback ExecResultCallback) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		e.logger.Error("failed to get hosts: %v", err)
		return err
	}

	e.logger.Debug("streaming exec on %d hosts: %v", len(hosts), req.Hosts)

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	if req.Timeout <= 0 {
		req.Timeout = 300 * time.Second
	}

	client, err := dispatchssh.NewClient(e.inv.GetConfig().SSH.KeyPath, e.getSSHClientOptions()...)
	if err != nil {
		e.logger.Error("failed to create SSH client: %v", err)
		return fmt.Errorf("failed to create SSH client: %w", err)
	}

	// Channel to collect final results
	results := make(chan *ExecResult, len(hosts))
	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	// Output callback function
	outputCallback := func(host string, typ string, data []byte) {
		if len(data) == 0 {
			return
		}
		
		if req.StreamCallback != nil {
			req.StreamCallback(host, typ, data)
			return
		}

		// Print output in real-time (default behavior)
		if typ == "error" {
			fmt.Printf("[%s] %s", host, data)
		} else if typ == "stderr" {
			fmt.Printf("[%s] stderr: %s", host, data)
		} else {
			fmt.Printf("[%s] %s", host, data)
		}
	}

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			result := &ExecResult{
				Host:      h.Address,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			cmd := e.buildCommand(req.Cmd, req.Env, req.Dir)

			// Streaming execution
			stdout, stderr, err := client.ExecStream(spec, cmd, req.Input, req.Timeout)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				results <- result
				outputCallback(h.Address, "error", []byte(err.Error()+"\n"))
				return
			}

			// WaitGroup for waiting for output reading to complete
			var readWg sync.WaitGroup
			readWg.Add(2)

			// Read stdout
			go func() {
				defer readWg.Done()
				buf := make([]byte, 1024)
				for {
					n, err := stdout.Read(buf)
					if n > 0 {
						outputCallback(h.Address, "stdout", buf[:n])
					}
					if err != nil {
						break
					}
				}
			}()

			// Read stderr
			go func() {
				defer readWg.Done()
				buf := make([]byte, 1024)
				for {
					n, err := stderr.Read(buf)
					if n > 0 {
						outputCallback(h.Address, "stderr", buf[:n])
					}
					if err != nil {
						break
					}
				}
			}()

			// Wait for command to complete and get exit code
			// Closing stdout triggers session.Wait()
			stdout.Close()
			stderr.Close()

			// Wait for all output reading to complete
			readWg.Wait()

			result.EndTime = time.Now()
			results <- result
		}(host)
	}

	// Wait for all hosts to complete and collect results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Call callback function
	for result := range results {
		callback(result)
	}

	return nil
}

// ExecStream executes command and returns streaming output
func (e *Executor) ExecStream(ctx context.Context, req *ExecRequest, outputCallback func(host string, typ string, data []byte)) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		return err
	}

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	if req.Timeout <= 0 {
		req.Timeout = 300 * time.Second
	}

	client, err := dispatchssh.NewClient(e.inv.GetConfig().SSH.KeyPath, e.getSSHClientOptions()...)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %w", err)
	}

	// 并发控制
	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			cmd := e.buildCommand(req.Cmd, req.Env, req.Dir)

			stdout, stderr, err := client.ExecStream(spec, cmd, req.Input, req.Timeout)
			if err != nil {
				outputCallback(h.Address, "error", []byte(err.Error()))
				return
			}

			// Read output
			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := stdout.Read(buf)
					if n > 0 {
						outputCallback(h.Address, "stdout", buf[:n])
					}
					if err != nil {
						break
					}
				}
			}()

			go func() {
				buf := make([]byte, 4096)
				for {
					n, err := stderr.Read(buf)
					if n > 0 {
						outputCallback(h.Address, "stderr", buf[:n])
					}
					if err != nil {
						break
					}
				}
			}()

			// Wait for command to complete
			<-ctx.Done()
		}(host)
	}

	wg.Wait()
	return nil
}

// CopyRequest copy file request
type CopyRequest struct {
	Hosts    []string
	Src      string
	Dest     string
	Mode     int // File permissions
	Parallel int
	Backup   bool // Whether to backup existing file
}

// CopyResult copy result
type CopyResult struct {
	Host      string
	BytesCopied int64
	StartTime  time.Time
	EndTime    time.Time
	Err        error
}

// Copy batch copies files
func (e *Executor) Copy(ctx context.Context, req *CopyRequest, callback func(*CopyResult)) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		return err
	}

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	if req.Mode == 0 {
		req.Mode = 0644
	}

	// Initialize connection cache for this operation
	if err := e.beginOperation(); err != nil {
		return err
	}
	defer e.endOperation()

	// Get source file size
	var srcSize int64
	if fi, err := os.Stat(req.Src); err == nil {
		srcSize = fi.Size()
	}

	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			result := &CopyResult{
				Host:      h.Address,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			// Get or create connection for this host
			conn, err := e.getConnection(spec)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				callback(result)
				return
			}

			// Perform backup if requested
			if req.Backup {
				backupCmd := fmt.Sprintf("if [ -f %s ]; then cp %s %s.bak; fi", req.Dest, req.Dest, req.Dest)
				e.execOnConn(conn, backupCmd, "", 10*time.Second)
			}

			err = e.copyFile(conn, req.Src, req.Dest, os.FileMode(req.Mode))
			result.EndTime = time.Now()

			if err != nil {
				result.Err = err
			} else {
				result.BytesCopied = srcSize
			}

			callback(result)
		}(host)
	}

	wg.Wait()
	return nil
}

// FetchRequest download file request
type FetchRequest struct {
	Hosts    []string
	Src      string
	Dest     string
	Parallel int
}

// FetchResult download result
type FetchResult struct {
	Host        string
	LocalPath   string
	BytesFetched int64
	StartTime   time.Time
	EndTime     time.Time
	Err         error
}

// Fetch batch downloads files
func (e *Executor) Fetch(ctx context.Context, req *FetchRequest, callback func(*FetchResult)) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		return err
	}

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	// Initialize connection cache for this operation
	if err := e.beginOperation(); err != nil {
		return err
	}
	defer e.endOperation()

	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			// Create separate target file for each host
			localPath := fmt.Sprintf("%s/%s", req.Dest, h.Address)
			if len(hosts) == 1 {
				localPath = req.Dest
			}

			result := &FetchResult{
				Host:      h.Address,
				LocalPath: localPath,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			// Get or create connection for this host
			conn, err := e.getConnection(spec)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				callback(result)
				return
			}

			bytesFetched, err := e.fetchFile(conn, req.Src, localPath)
			result.EndTime = time.Now()

			if err != nil {
				result.Err = err
			} else {
				result.BytesFetched = bytesFetched
			}

			callback(result)
		}(host)
	}

	wg.Wait()
	return nil
}

// DeleteRequest delete file request
type DeleteRequest struct {
	Hosts    []string
	Path     string // File path to delete
	Parallel int
}

// DeleteResult delete result
type DeleteResult struct {
	Host      string
	Path      string
	StartTime time.Time
	EndTime   time.Time
	Err       error
}

// Delete batch deletes files
func (e *Executor) Delete(ctx context.Context, req *DeleteRequest, callback func(*DeleteResult)) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		return err
	}

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	// Initialize connection cache for this operation
	if err := e.beginOperation(); err != nil {
		return err
	}
	defer e.endOperation()

	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			result := &DeleteResult{
				Host:      h.Address,
				Path:      req.Path,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			// Get or create connection for this host
			conn, err := e.getConnection(spec)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				callback(result)
				return
			}

			cmd := fmt.Sprintf("rm -rf %s", req.Path)
			execResult, err := e.execOnConn(conn, cmd, "", 30*time.Second)
			result.EndTime = time.Now()

			if err != nil {
				result.Err = err
			} else if execResult.ExitCode != 0 {
				result.Err = fmt.Errorf("exit code %d: %s", execResult.ExitCode, string(execResult.Error))
			}

			callback(result)
		}(host)
	}

	wg.Wait()
	return nil
}

// UpdateRequest update file request
type UpdateRequest struct {
	Hosts    []string
	Src      string // Source file path (local)
	Dest     string // Target file path (remote)
	Mode     int    // File permissions
	Parallel int
	Backup   bool // Whether to backup existing file
}

// UpdateResult update result
type UpdateResult struct {
	Host        string
	BytesCopied int64
	Skipped     bool // File unchanged, skipped
	StartTime   time.Time
	EndTime     time.Time
	Err         error
}

// Update batch updates files (only copies when changed)
func (e *Executor) Update(ctx context.Context, req *UpdateRequest, callback func(*UpdateResult)) error {
	hosts, err := e.inv.GetHosts(req.Hosts)
	if err != nil {
		return err
	}

	if req.Parallel <= 0 {
		req.Parallel = e.inv.GetDefaultParallel()
	}

	if req.Mode == 0 {
		req.Mode = 0644
	}

	// Initialize connection cache for this operation
	if err := e.beginOperation(); err != nil {
		return err
	}
	defer e.endOperation()

	// Read local file content
	localContent, err := os.ReadFile(req.Src)
	if err != nil {
		return fmt.Errorf("failed to read local file: %w", err)
	}

	// Calculate local file checksum
	hash := sha256.Sum256(localContent)
	localChecksum := fmt.Sprintf("%x", hash)

	sem := make(chan struct{}, req.Parallel)
	var wg sync.WaitGroup

	for _, host := range hosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		wg.Add(1)
		sem <- struct{}{}

		go func(h inventory.Host) {
			defer wg.Done()
			defer func() { <-sem }()

			result := &UpdateResult{
				Host:      h.Address,
				Skipped:   false,
				StartTime: time.Now(),
			}

			spec := dispatchssh.HostSpec{
				Address:    h.Address,
				User:       h.User,
				Port:       h.Port,
				KeyPath:    h.KeyPath,
				UserSet:    h.UserSet,
				PortSet:    h.PortSet,
				KeyPathSet: h.KeyPathSet,
			}

			// Get or create connection for this host
			conn, err := e.getConnection(spec)
			if err != nil {
				result.Err = err
				result.EndTime = time.Now()
				callback(result)
				return
			}

			// Check if remote file exists and its checksum
			// Use sha256sum for reliable change detection
			checkCmd := fmt.Sprintf("if [ -f %s ]; then sha256sum %s 2>/dev/null | cut -d' ' -f1; else echo -1; fi",
				req.Dest, req.Dest)

			execResult, err := e.execOnConn(conn, checkCmd, "", 30*time.Second)

			shouldCopy := true
			if err == nil && execResult.ExitCode == 0 {
				remoteChecksum := strings.TrimSpace(string(execResult.Output))
				if remoteChecksum == localChecksum {
					shouldCopy = false
					result.Skipped = true
				}
			}

			if shouldCopy {
				// Perform backup if requested and file exists
				if req.Backup {
					backupCmd := fmt.Sprintf("if [ -f %s ]; then cp %s %s.bak; fi", req.Dest, req.Dest, req.Dest)
					e.execOnConn(conn, backupCmd, "", 10*time.Second)
				}

				err = e.copyFile(conn, req.Src, req.Dest, os.FileMode(req.Mode))
				result.EndTime = time.Now()

				if err != nil {
					result.Err = err
				} else {
					result.BytesCopied = int64(len(localContent))
				}
			} else {
				result.EndTime = time.Now()
			}

			callback(result)
		}(host)
	}

	wg.Wait()
	return nil
}

// buildCommand builds complete command
func (e *Executor) buildCommand(cmd string, env map[string]string, dir string) string {
	fullCmd := ""

	// Set working directory
	if dir != "" {
		fullCmd = fmt.Sprintf("cd %s && ", dir)
	}

	// Set environment variables
	for k, v := range env {
		fullCmd += fmt.Sprintf("%s='%s' ", k, v)
	}

	// Add command
	fullCmd += cmd

	// Use shell to execute
	shell := e.inv.GetConfig().Exec.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	return fmt.Sprintf("%s -c \"%s\"", shell, fullCmd)
}
