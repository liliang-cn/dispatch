// Package dispatch provides a simple SSH batch operation library for executing
// commands, copying files, and fetching files from multiple remote servers in parallel.
//
// The package can be used as a Go library or as a CLI tool. It supports reading
// host configurations from ~/.ssh/config, making it easy to use without any
// additional configuration.
//
// # Configuration Priority
//
// Host configurations are resolved in the following order (highest to lowest):
//
//  1. ~/.dispatch/config.toml host-level settings
//  2. ~/.dispatch/config.toml group-level settings
//  3. ~/.ssh/config entries (User, Port, IdentityFile, HostName)
//  4. Default values (current user, port 22, auto-detect SSH keys)
//
// # Basic Usage (No Configuration Required)
//
// If ~/.dispatch/config.toml does not exist, dispatch automatically reads host
// configurations from ~/.ssh/config:
//
//	client, _ := dispatch.New(nil)
//
//	// Use host names defined in ~/.ssh/config
//	result, _ := client.Exec(ctx, []string{"orange1", "orange2"}, "uptime")
//
// # Usage With Custom Configuration
//
//	client, _ := dispatch.New(&dispatch.Config{
//	    ConfigPath: "/path/to/config.toml", // Optional, defaults to ~/.dispatch/config.toml
//	})
//
// # Example
//
//	package main
//
//	import (
//	    "context"
//	    "fmt"
//	    "time"
//
//	    "github.com/liliang-cn/dispatch/pkg/dispatch"
//	)
//
//	func main() {
//	    // Create client (reads from ~/.ssh/config by default)
//	    client, err := dispatch.New(nil)
//	    if err != nil {
//	        panic(err)
//	    }
//
//	    ctx := context.Background()
//
//	    // Execute command on hosts defined in ~/.ssh/config
//	    result, err := client.Exec(ctx, []string{"orange1", "orange2"}, "uptime",
//	        dispatch.WithTimeout(30*time.Second),
//	        dispatch.WithParallel(5),
//	    )
//	    if err != nil {
//	        panic(err)
//	    }
//
//	    for host, r := range result.Hosts {
//	        fmt.Printf("%s: %s\n", host, r.Output)
//	    }
//	}
package dispatch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/executor"
)

// Dispatch is the main client for SSH batch operations.
// It can be used as a CLI tool or as a Go library.
//
// Create a new client with New() or NewWithInventory().
type Dispatch struct {
	inv      *inventory.Inventory
	executor *executor.Executor
	mu       sync.RWMutex
}

// Config contains configuration options for creating a Dispatch client.
// All fields are optional - if not specified, defaults are used.
//
// If ConfigPath is empty and ~/.dispatch/config.toml does not exist,
// the client will read host configurations from ~/.ssh/config.
type Config struct {
	// ConfigPath is the path to the TOML configuration file.
	// If empty, defaults to ~/.dispatch/config.toml.
	// If the file does not exist, ~/.ssh/config is used instead.
	ConfigPath string
	// SSH contains default SSH settings that override config file values.
	SSH *SSHConfig
	// Exec contains default execution settings that override config file values.
	Exec *ExecConfig
}

// SSHConfig SSH 配置
type SSHConfig struct {
	User    string
	Port    int
	KeyPath string
	Timeout int // 秒
}

// ExecConfig 执行配置
type ExecConfig struct {
	Parallel int
	Timeout  int // 秒
	Shell    string
}

// New creates a new Dispatch client.
//
// If cfg is nil or ConfigPath is empty, the client will:
//  1. Try to load ~/.dispatch/config.toml (if it exists)
//  2. Fall back to reading host configurations from ~/.ssh/config
//
// This means you can use dispatch without any configuration file:
//
//	client, _ := dispatch.New(nil)
//	result, _ := client.Exec(ctx, []string{"myserver"}, "uptime")
//
// Where "myserver" is defined in your ~/.ssh/config.
func New(cfg *Config) (*Dispatch, error) {
	configPath := ""
	if cfg != nil {
		configPath = cfg.ConfigPath
	}

	inv, err := inventory.New(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create inventory: %w", err)
	}

	// 如果提供了配置，覆盖默认值
	if cfg != nil && cfg.SSH != nil {
		invCfg := inv.GetConfig()
		if cfg.SSH.User != "" {
			invCfg.SSH.User = cfg.SSH.User
		}
		if cfg.SSH.Port > 0 {
			invCfg.SSH.Port = cfg.SSH.Port
		}
		if cfg.SSH.KeyPath != "" {
			invCfg.SSH.KeyPath = cfg.SSH.KeyPath
		}
		if cfg.SSH.Timeout > 0 {
			invCfg.SSH.Timeout = fmt.Sprintf("%ds", cfg.SSH.Timeout)
		}
	}

	if cfg != nil && cfg.Exec != nil {
		invCfg := inv.GetConfig()
		if cfg.Exec.Parallel > 0 {
			invCfg.Exec.Parallel = cfg.Exec.Parallel
		}
		if cfg.Exec.Timeout > 0 {
			invCfg.Exec.Timeout = fmt.Sprintf("%ds", cfg.Exec.Timeout)
		}
		if cfg.Exec.Shell != "" {
			invCfg.Exec.Shell = cfg.Exec.Shell
		}
	}

	return &Dispatch{
		inv:      inv,
		executor: executor.NewExecutor(inv),
	}, nil
}

// NewWithInventory 使用已有的 inventory 创建客户端
func NewWithInventory(inv *inventory.Inventory) *Dispatch {
	return &Dispatch{
		inv:      inv,
		executor: executor.NewExecutor(inv),
	}
}

// ProgressInfo represents file transfer progress.
type ProgressInfo struct {
	Host    string
	Action  string // "upload" or "download"
	Current int64
	Total   int64
}

// Exec executes a command on the specified hosts.
//
// The hosts parameter can contain:
//   - Host names defined in ~/.ssh/config (e.g., "orange1")
//   - Host group names defined in ~/.dispatch/config.toml
//   - Direct IP addresses or hostnames
//   - Wildcard patterns matching ~/.ssh/config entries (e.g., "orange*")
//
// Use functional options to configure parallelism, timeout, environment variables, etc.
func (d *Dispatch) Exec(ctx context.Context, hosts []string, cmd string, opts ...ExecOption) (*ExecResult, error) {
	options := &execOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.ExecRequest{
		Hosts: hosts,
		Cmd:   cmd,
	}

	if options.parallel > 0 {
		req.Parallel = options.parallel
	}
	if options.timeout > 0 {
		req.Timeout = options.timeout
	}
	if options.env != nil {
		req.Env = options.env
	}
	if options.dir != "" {
		req.Dir = options.dir
	}
	if options.input != "" {
		req.Input = options.input
	}
	if options.streamCallback != nil {
		req.StreamCallback = options.streamCallback
		req.Stream = true
	}

	result := &ExecResult{
		Hosts:    make(map[string]*HostResult),
		StartTime: time.Now(),
	}

	var mu sync.Mutex
	callback := func(r *executor.ExecResult) {
		mu.Lock()
		defer mu.Unlock()

		result.Hosts[r.Host] = &HostResult{
			Host:      r.Host,
			Output:    r.Output,
			Error:     r.Error,
			ExitCode:  r.ExitCode,
			ErrorMsg:  r.Err,
			Success:   r.ExitCode == 0 && r.Err == nil,
			Duration:  r.EndTime.Sub(r.StartTime),
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
	}

	if err := d.executor.Exec(ctx, req, callback); err != nil {
		return result, err
	}

	result.EndTime = time.Now()
	return result, nil
}

// ExecOption 执行选项
type ExecOption func(*execOptions)

type execOptions struct {
	parallel int
	timeout  time.Duration
	env      map[string]string
	dir      string
	input    string
	streamCallback func(host, streamType string, data []byte)
}

// WithParallel 设置并发数
func WithParallel(n int) ExecOption {
	return func(o *execOptions) {
		o.parallel = n
	}
}

// WithInput 设置标准输入
func WithInput(input string) ExecOption {
	return func(o *execOptions) {
		o.input = input
	}
}

// WithStreamCallback 设置流式输出回调
func WithStreamCallback(callback func(host, streamType string, data []byte)) ExecOption {
	return func(o *execOptions) {
		o.streamCallback = callback
	}
}

// WithTimeout 设置超时时间
func WithTimeout(d time.Duration) ExecOption {
	return func(o *execOptions) {
		o.timeout = d
	}
}

// WithEnv 设置环境变量
func WithEnv(env map[string]string) ExecOption {
	return func(o *execOptions) {
		o.env = env
	}
}

// WithDir 设置工作目录
func WithDir(dir string) ExecOption {
	return func(o *execOptions) {
		o.dir = dir
	}
}

// ExecResult contains the results of executing a command on multiple hosts.
// It provides aggregate information about the execution and per-host results.
type ExecResult struct {
	// Hosts maps each host address to its individual execution result.
	Hosts map[string]*HostResult
	// StartTime is when the execution began.
	StartTime time.Time
	// EndTime is when all executions completed.
	EndTime time.Time
}

// HostResult contains the result of executing a command on a single host.
type HostResult struct {
	// Host is the address of the host.
	Host string
	// Output contains the combined stdout and stderr output.
	Output []byte
	// Error contains the standard error output.
	Error []byte
	// ExitCode is the exit status of the command.
	ExitCode int
	// ErrorMsg contains any internal error (like connection failure).
	ErrorMsg error
	// Success is true if ExitCode is 0 and ErrorMsg is nil.
	Success bool
	// Duration is how long the command took to execute.
	Duration time.Duration
	// StartTime is when execution began for this host.
	StartTime time.Time
	// EndTime is when execution completed for this host.
	EndTime time.Time
}

// AllSuccess returns true if all host executions completed successfully.
// A host is considered successful if its ExitCode is 0.
func (r *ExecResult) AllSuccess() bool {
	for _, h := range r.Hosts {
		if !h.Success {
			return false
		}
	}
	return true
}

// FailedHosts returns a slice of host addresses where execution failed.
// A host is considered failed if its ExitCode is non-zero.
func (r *ExecResult) FailedHosts() []string {
	var failed []string
	for _, h := range r.Hosts {
		if !h.Success {
			failed = append(failed, h.Host)
		}
	}
	return failed
}

// Copy copies a file to the specified remote hosts.
//
// The hosts parameter can contain host names from ~/.ssh/config, group names
// from ~/.dispatch/config.toml, direct addresses, or wildcard patterns.
func (d *Dispatch) Copy(ctx context.Context, hosts []string, src, dest string, opts ...CopyOption) (*CopyResult, error) {
	options := &copyOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.CopyRequest{
		Hosts: hosts,
		Src:   src,
		Dest:  dest,
	}

	if options.parallel > 0 {
		req.Parallel = options.parallel
	}
	if options.mode > 0 {
		req.Mode = options.mode
	}
	req.Backup = options.backup
	
	if options.progressCallback != nil {
		req.ProgressCallback = func(info executor.ProgressInfo) {
			options.progressCallback(ProgressInfo{
				Host:    info.Host,
				Action:  info.Action,
				Current: info.Current,
				Total:   info.Total,
			})
		}
	}

	result := &CopyResult{
		Hosts:     make(map[string]*CopyHostResult),
		StartTime: time.Now(),
	}

	var mu sync.Mutex
	callback := func(r *executor.CopyResult) {
		mu.Lock()
		defer mu.Unlock()

		result.Hosts[r.Host] = &CopyHostResult{
			Host:        r.Host,
			BytesCopied: r.BytesCopied,
			Success:     r.Err == nil,
			Error:       r.Err,
			Duration:    r.EndTime.Sub(r.StartTime),
			StartTime:   r.StartTime,
			EndTime:     r.EndTime,
		}
	}

	if err := d.executor.Copy(ctx, req, callback); err != nil {
		return result, err
	}

	result.EndTime = time.Now()
	return result, nil
}

// CopyOption 复制选项
type CopyOption func(*copyOptions)

type copyOptions struct {
	parallel int
	mode     int
	backup   bool
	progressCallback func(info ProgressInfo)
}

// WithCopyMode 设置文件权限
func WithCopyMode(mode int) CopyOption {
	return func(o *copyOptions) {
		o.mode = mode
	}
}

// WithBackup 设置是否备份原文件
func WithBackup(backup bool) CopyOption {
	return func(o *copyOptions) {
		o.backup = backup
	}
}

// WithCopyProgress 设置复制进度回调
func WithCopyProgress(callback func(info ProgressInfo)) CopyOption {
	return func(o *copyOptions) {
		o.progressCallback = callback
	}
}

// CopyResult contains the results of copying a file to multiple hosts.
type CopyResult struct {
	// Hosts maps each host address to its individual copy result.
	Hosts map[string]*CopyHostResult
	// StartTime is when the copy operation began.
	StartTime time.Time
	// EndTime is when all copy operations completed.
	EndTime time.Time
}

// CopyHostResult contains the result of copying a file to a single host.
type CopyHostResult struct {
	// Host is the address of the host.
	Host string
	// BytesCopied is the number of bytes copied to the host.
	BytesCopied int64
	// Success is true if the file was copied successfully.
	Success bool
	// Error contains any error that occurred during the copy.
	Error error
	// Duration is how long the copy operation took.
	Duration time.Duration
	// StartTime is when the copy began.
	StartTime time.Time
	// EndTime is when the copy completed.
	EndTime time.Time
}

// Update updates a file on remote hosts only if the content has changed.
//
// The hosts parameter can contain host names from ~/.ssh/config, group names
// from ~/.dispatch/config.toml, direct addresses, or wildcard patterns.
func (d *Dispatch) Update(ctx context.Context, hosts []string, src, dest string, opts ...UpdateOption) (*UpdateResult, error) {
	options := &updateOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.UpdateRequest{
		Hosts: hosts,
		Src:   src,
		Dest:  dest,
	}

	if options.parallel > 0 {
		req.Parallel = options.parallel
	}
	if options.mode > 0 {
		req.Mode = options.mode
	}
	req.Backup = options.backup

	if options.progressCallback != nil {
		req.ProgressCallback = func(info executor.ProgressInfo) {
			options.progressCallback(ProgressInfo{
				Host:    info.Host,
				Action:  info.Action,
				Current: info.Current,
				Total:   info.Total,
			})
		}
	}

	result := &UpdateResult{
		Hosts:     make(map[string]*UpdateHostResult),
		StartTime: time.Now(),
	}

	var mu sync.Mutex
	callback := func(r *executor.UpdateResult) {
		mu.Lock()
		defer mu.Unlock()

		result.Hosts[r.Host] = &UpdateHostResult{
			Host:        r.Host,
			BytesCopied: r.BytesCopied,
			Skipped:     r.Skipped,
			Success:     r.Err == nil,
			Error:       r.Err,
			Duration:    r.EndTime.Sub(r.StartTime),
			StartTime:   r.StartTime,
			EndTime:     r.EndTime,
		}
	}

	if err := d.executor.Update(ctx, req, callback); err != nil {
		return result, err
	}

	result.EndTime = time.Now()
	return result, nil
}

// UpdateOption 更新选项
type UpdateOption func(*updateOptions)

type updateOptions struct {
	parallel int
	mode     int
	backup   bool
	progressCallback func(info ProgressInfo)
}

// WithUpdateMode 设置文件权限
func WithUpdateMode(mode int) UpdateOption {
	return func(o *updateOptions) {
		o.mode = mode
	}
}

// WithUpdateBackup 设置是否备份原文件
func WithUpdateBackup(backup bool) UpdateOption {
	return func(o *updateOptions) {
		o.backup = backup
	}
}

// WithUpdateParallel 设置并发数
func WithUpdateParallel(n int) UpdateOption {
	return func(o *updateOptions) {
		o.parallel = n
	}
}

// WithUpdateProgress 设置更新进度回调
func WithUpdateProgress(callback func(info ProgressInfo)) UpdateOption {
	return func(o *updateOptions) {
		o.progressCallback = callback
	}
}

// UpdateResult contains the results of updating a file on multiple hosts.
// Update only copies the file if the content has changed.
type UpdateResult struct {
	// Hosts maps each host address to its individual update result.
	Hosts map[string]*UpdateHostResult
	// StartTime is when the update operation began.
	StartTime time.Time
	// EndTime is when all update operations completed.
	EndTime time.Time
}

// UpdateHostResult contains the result of updating a file on a single host.
type UpdateHostResult struct {
	// Host is the address of the host.
	Host string
	// BytesCopied is the number of bytes copied (0 if skipped).
	BytesCopied int64
	// Skipped is true if the file was not copied because it was unchanged.
	Skipped bool
	// Success is true if the update was successful or skipped.
	Success bool
	// Error contains any error that occurred during the update.
	Error error
	// Duration is how long the update operation took.
	Duration time.Duration
	// StartTime is when the update began.
	StartTime time.Time
	// EndTime is when the update completed.
	EndTime time.Time
}

// Fetch downloads files from remote hosts to the local machine.
//
// The hosts parameter specifies which hosts to fetch from, using group names
// or direct host addresses. The src parameter is the path to the file on the
// remote hosts. The dest parameter is the local directory where files will be saved.
//
// When fetching from multiple hosts, each file is saved with the host address
// as the filename to avoid conflicts. When fetching from a single host, the
// original filename is preserved.
func (d *Dispatch) Fetch(ctx context.Context, hosts []string, src, dest string, opts ...FetchOption) (*FetchResult, error) {
	options := &fetchOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.FetchRequest{
		Hosts: hosts,
		Src:   src,
		Dest:  dest,
	}

	if options.parallel > 0 {
		req.Parallel = options.parallel
	}

	if options.progressCallback != nil {
		req.ProgressCallback = func(info executor.ProgressInfo) {
			options.progressCallback(ProgressInfo{
				Host:    info.Host,
				Action:  info.Action,
				Current: info.Current,
				Total:   info.Total,
			})
		}
	}

	result := &FetchResult{
		Hosts:     make(map[string]*FetchHostResult),
		StartTime: time.Now(),
	}

	var mu sync.Mutex
	callback := func(r *executor.FetchResult) {
		mu.Lock()
		defer mu.Unlock()

		result.Hosts[r.Host] = &FetchHostResult{
			Host:         r.Host,
			LocalPath:    r.LocalPath,
			BytesFetched: r.BytesFetched,
			Success:      r.Err == nil,
			Error:        r.Err,
			Duration:     r.EndTime.Sub(r.StartTime),
			StartTime:    r.StartTime,
			EndTime:      r.EndTime,
		}
	}

	if err := d.executor.Fetch(ctx, req, callback); err != nil {
		return result, err
	}

	result.EndTime = time.Now()
	return result, nil
}

// FetchOption 下载选项
type FetchOption func(*fetchOptions)

type fetchOptions struct {
	parallel int
	progressCallback func(info ProgressInfo)
}

// WithFetchProgress 设置下载进度回调
func WithFetchProgress(callback func(info ProgressInfo)) FetchOption {
	return func(o *fetchOptions) {
		o.progressCallback = callback
	}
}

// FetchResult contains the results of fetching a file from multiple hosts.
type FetchResult struct {
	// Hosts maps each host address to its individual fetch result.
	Hosts map[string]*FetchHostResult
	// StartTime is when the fetch operation began.
	StartTime time.Time
	// EndTime is when all fetch operations completed.
	EndTime time.Time
}

// FetchHostResult contains the result of fetching a file from a single host.
type FetchHostResult struct {
	// Host is the address of the host.
	Host string
	// LocalPath is where the file was saved locally.
	LocalPath string
	// BytesFetched is the number of bytes fetched.
	BytesFetched int64
	// Success is true if the file was fetched successfully.
	Success bool
	// Error contains any error that occurred during the fetch.
	Error error
	// Duration is how long the fetch operation took.
	Duration time.Duration
	// StartTime is when the fetch began.
	StartTime time.Time
	// EndTime is when the fetch completed.
	EndTime time.Time
}

// GetInventory returns the underlying inventory used by this Dispatch client.
// This provides access to advanced inventory management features.
func (d *Dispatch) GetInventory() *inventory.Inventory {
	return d.inv
}

// GetHosts resolves host patterns to actual host configurations.
//
// Patterns can be:
//   - Host names defined in ~/.ssh/config
//   - Group names defined in ~/.dispatch/config.toml
//   - Direct host addresses or IP addresses
//   - Wildcard patterns (e.g., "orange*")
func (d *Dispatch) GetHosts(patterns []string) ([]inventory.Host, error) {
	return d.inv.GetHosts(patterns)
}

// GetAllGroups returns all host groups defined in the configuration.
// The returned map maps group names to slices of host addresses.
func (d *Dispatch) GetAllGroups() map[string][]string {
	return d.inv.GetAllGroups()
}

// ========== Stats ==========

// StatsOption is a function that configures a Stats operation.
type StatsOption func(*statsOptions)

type statsOptions struct {
	parallel int
}

// WithStatsParallel sets the parallelism for stats operations.
func WithStatsParallel(n int) StatsOption {
	return func(o *statsOptions) {
		o.parallel = n
	}
}

// Stats gets file stats from remote hosts.
func (d *Dispatch) Stats(ctx context.Context, hosts []string, path string, opts ...StatsOption) (*StatsResult, error) {
	options := &statsOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.StatsRequest{
		Hosts:    hosts,
		Path:     path,
		Parallel: options.parallel,
	}

	result := &StatsResult{
		Hosts:     make(map[string]*StatsHostResult),
		StartTime: time.Now(),
	}

	mu := sync.Mutex{}

	err := d.executor.Stats(ctx, req, func(r *executor.StatsResult) {
		mu.Lock()
		defer mu.Unlock()

		hostResult := &StatsHostResult{
			Host:      r.Host,
			Path:      r.Path,
			Exists:    r.Exists,
			IsDir:     r.IsDir,
			Size:      r.Size,
			Mode:      r.Mode,
			ModTime:   r.ModTime,
			Owner:     r.Owner,
			Group:     r.Group,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
		if r.Err != nil {
			hostResult.Error = r.Err
			hostResult.Success = false
		} else {
			hostResult.Success = true
		}
		result.Hosts[r.Host] = hostResult
	})

	result.EndTime = time.Now()
	return result, err
}

// StatsResult contains the results of getting file stats from multiple hosts.
type StatsResult struct {
	Hosts     map[string]*StatsHostResult
	StartTime time.Time
	EndTime   time.Time
}

// StatsHostResult contains the file stats from a single host.
type StatsHostResult struct {
	Host      string
	Path      string
	Exists    bool
	IsDir     bool
	Size      int64
	Mode      int64
	ModTime   int64
	Owner     string
	Group     string
	Success   bool
	Error     error
	StartTime time.Time
	EndTime   time.Time
}

// ========== Read ==========

// ReadOption is a function that configures a Read operation.
type ReadOption func(*readOptions)

type readOptions struct {
	parallel int
	offset   int64
	limit    int64
}

// WithReadParallel sets the parallelism for read operations.
func WithReadParallel(n int) ReadOption {
	return func(o *readOptions) {
		o.parallel = n
	}
}

// WithReadOffset sets the starting offset for reading.
func WithReadOffset(n int64) ReadOption {
	return func(o *readOptions) {
		o.offset = n
	}
}

// WithReadLimit sets the maximum bytes to read (0 = all).
func WithReadLimit(n int64) ReadOption {
	return func(o *readOptions) {
		o.limit = n
	}
}

// Read reads file content from remote hosts.
func (d *Dispatch) Read(ctx context.Context, hosts []string, path string, opts ...ReadOption) (*ReadResult, error) {
	options := &readOptions{}
	for _, opt := range opts {
		opt(options)
	}

	req := &executor.ReadRequest{
		Hosts:    hosts,
		Path:     path,
		Parallel: options.parallel,
		Offset:   options.offset,
		Limit:    options.limit,
	}

	result := &ReadResult{
		Hosts:     make(map[string]*ReadHostResult),
		StartTime: time.Now(),
	}

	mu := sync.Mutex{}

	err := d.executor.Read(ctx, req, func(r *executor.ReadResult) {
		mu.Lock()
		defer mu.Unlock()

		hostResult := &ReadHostResult{
			Host:      r.Host,
			Path:      r.Path,
			Content:   r.Content,
			Offset:    r.Offset,
			IsBinary:  r.IsBinary,
			TotalSize: r.TotalSize,
			StartTime: r.StartTime,
			EndTime:   r.EndTime,
		}
		if r.Err != nil {
			hostResult.Error = r.Err
			hostResult.Success = false
		} else {
			hostResult.Success = true
		}
		result.Hosts[r.Host] = hostResult
	})

	result.EndTime = time.Now()
	return result, err
}

// ReadResult contains the results of reading a file from multiple hosts.
type ReadResult struct {
	Hosts     map[string]*ReadHostResult
	StartTime time.Time
	EndTime   time.Time
}

// ReadHostResult contains the file content from a single host.
type ReadHostResult struct {
	Host      string
	Path      string
	Content   string
	Offset    int64
	IsBinary  bool
	TotalSize int64
	Success   bool
	Error     error
	StartTime time.Time
	EndTime   time.Time
}
