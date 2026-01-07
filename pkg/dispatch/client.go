package dispatch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/executor"
)

// Dispatch 是主客户端，既可以作为 CLI 使用，也可以作为库使用
type Dispatch struct {
	inv      *inventory.Inventory
	executor *executor.Executor
	mu       sync.RWMutex
}

// Config 创建 Dispatch 客户端的配置
type Config struct {
	ConfigPath string           // 配置文件路径，空则使用默认
	SSH       *SSHConfig       // SSH 默认配置
	Exec      *ExecConfig      // 执行默认配置
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

// New 创建新的 Dispatch 客户端
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

// Exec 在指定主机上执行命令
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
			Success:   r.ExitCode == 0,
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
	// Success is true if ExitCode is 0.
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

// Copy 复制文件到远程主机
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

// Update 更新远程文件（仅当变更时）
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
// Patterns can be group names defined in the config or direct host addresses.
func (d *Dispatch) GetHosts(patterns []string) ([]inventory.Host, error) {
	return d.inv.GetHosts(patterns)
}

// GetAllGroups returns all host groups defined in the configuration.
// The returned map maps group names to slices of host addresses.
func (d *Dispatch) GetAllGroups() map[string][]string {
	return d.inv.GetAllGroups()
}
