// Package server provides a gRPC server implementation for the dispatch service.
//
// The Server implements the Dispatch gRPC service defined in proto/dispatch.proto,
// supporting remote execution of commands, file operations (copy, fetch), and job management.
//
// Job Management
//
// Each operation (Exec, Copy, Fetch) creates a Job with a unique ID that tracks:
//   - Job type (Exec/Copy/Fetch)
//   - Status (Pending/Running/Completed/Failed/Cancelled)
//   - Per-host results with timing and output
//
// Streaming Results
//
// Operations use gRPC streaming to send results in real-time as they complete,
// allowing clients to process output without waiting for all hosts to finish.
//
// Example Usage:
//
//	server, err := server.NewServer("")
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	listener, _ := net.Listen("tcp", ":50051")
//	s := grpc.NewServer()
//	pb.RegisterDispatchServer(s, server)
//	s.Serve(listener)
package server

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	"github.com/liliang-cn/dispatch/pkg/executor"
	pb "github.com/liliang-cn/dispatch/proto"
)

// Server implements the gRPC Dispatch service.
// It manages SSH operations across multiple hosts with job tracking and streaming results.
type Server struct {
	pb.UnimplementedDispatchServer
	// inv is the inventory containing host configurations.
	inv       *inventory.Inventory
	// executor handles parallel execution across hosts.
	executor  *executor.Executor
	// jobs stores active and completed jobs indexed by job ID.
	jobs      map[string]*Job
	// jobMu protects concurrent access to jobs map.
	jobMu     sync.RWMutex
}

// Job represents a single dispatch operation (Exec, Copy, or Fetch).
// It tracks status, timing, and per-host results throughout the operation lifecycle.
type Job struct {
	// ID is the unique identifier for this job.
	ID          string
	// Type indicates the operation kind (Exec/Copy/Fetch).
	Type        JobType
	// Status indicates the current job state.
	Status      JobStatus
	// Hosts is the list of target host addresses.
	Hosts       []string
	// CreatedAt is when the job was created.
	CreatedAt   time.Time
	// StartedAt is when execution began (nil if not started).
	StartedAt   *time.Time
	// CompletedAt is when the job finished (nil if not completed).
	CompletedAt *time.Time
	// Results maps host address to execution results.
	Results     map[string]*HostResult
	// mu protects concurrent access to job fields.
	mu          sync.RWMutex
}

// JobType represents the kind of operation a job performs.
type JobType int

// JobStatus represents the current state of a job.
type JobStatus int

const (
	JobTypeExec JobType = iota // Exec command operation
	JobTypeCopy                 // Copy file operation
	JobTypeFetch                // Fetch file operation
)

const (
	StatusPending JobStatus = iota // Job is queued but not started
	StatusRunning                 // Job is currently executing
	StatusCompleted               // Job finished successfully
	StatusFailed                  // Job finished with errors
	StatusCancelled               // Job was cancelled before completion
)

// HostResult contains the result of an operation on a single host.
type HostResult struct {
	// Host is the address of the host.
	Host       string
	// Success indicates whether the operation succeeded.
	Success    bool
	// Error contains any error message if the operation failed.
	Error      string
	// DurationMs is the operation duration in milliseconds.
	DurationMs int64
	// Output contains the standard output (for Exec operations).
	Output     []byte
}

// NewServer creates a new gRPC server with the given configuration path.
// If configPath is empty, the default path ~/.dispatch/config.toml is used.
func NewServer(configPath string) (*Server, error) {
	inv, err := inventory.New(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create inventory: %w", err)
	}

	return &Server{
		inv:      inv,
		executor: executor.NewExecutor(inv),
		jobs:     make(map[string]*Job),
	}, nil
}

// Exec executes a command on multiple hosts with streaming results.
// It creates a job and streams execution results as each host completes.
func (s *Server) Exec(req *pb.ExecRequest, stream pb.Dispatch_ExecServer) error {
	hosts := req.Hosts
	command := req.Command
	parallel := int(req.Parallel)
	timeoutSecs := int(req.TimeoutSeconds)
	env := req.Env
	workDir := req.WorkDir

	job := &Job{
		ID:        generateJobID(),
		Type:      JobTypeExec,
		Status:    StatusRunning,
		Hosts:     hosts,
		CreatedAt: time.Now(),
		Results:   make(map[string]*HostResult),
	}

	s.jobMu.Lock()
	s.jobs[job.ID] = job
	s.jobMu.Unlock()

	now := time.Now()
	job.StartedAt = &now

	ctx := stream.Context()
	execReq := &executor.ExecRequest{
		Hosts:    hosts,
		Cmd:      command,
		Env:      env,
		Dir:      workDir,
		Parallel: parallel,
		Timeout:  time.Duration(timeoutSecs) * time.Second,
		Input:    req.Input,
	}

	// 使用回调收集结果并发送到流
	callback := func(result *executor.ExecResult) {
		job.mu.Lock()
		defer job.mu.Unlock()

		duration := result.EndTime.Sub(result.StartTime)
		job.Results[result.Host] = &HostResult{
			Host:       result.Host,
			Success:    result.ExitCode == 0,
			Error:      errorMsg(result),
			DurationMs: duration.Milliseconds(),
			Output:     result.Output,
		}

		// 发送输出
		if len(result.Output) > 0 {
			stream.Send(&pb.ExecResponse{
				Host: result.Host,
				Type: pb.ExecResponse_STDOUT,
				Data: result.Output,
			})
		}
		if len(result.Error) > 0 {
			stream.Send(&pb.ExecResponse{
				Host: result.Host,
				Type: pb.ExecResponse_STDERR,
				Data: result.Error,
			})
		}

		// 发送完成信号
		stream.Send(&pb.ExecResponse{
			Host:     result.Host,
			Finished: true,
			ExitCode: int32(result.ExitCode),
		})
	}

	if err := s.executor.Exec(ctx, execReq, callback); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.mu.Unlock()
		return err
	}

	job.mu.Lock()
	job.Status = StatusCompleted
	completed := time.Now()
	job.CompletedAt = &completed
	job.mu.Unlock()

	return nil
}

// Copy copies a file to multiple hosts with streaming progress updates.
// It creates a job and streams copy results as each host completes.
func (s *Server) Copy(req *pb.CopyRequest, stream pb.Dispatch_CopyServer) error {
	hosts := req.Hosts
	src := req.Src
	dest := req.Dest
	parallel := int(req.Parallel)
	mode := int(req.Mode)

	job := &Job{
		ID:        generateJobID(),
		Type:      JobTypeCopy,
		Status:    StatusRunning,
		Hosts:     hosts,
		CreatedAt: time.Now(),
		Results:   make(map[string]*HostResult),
	}

	s.jobMu.Lock()
	s.jobs[job.ID] = job
	s.jobMu.Unlock()

	now := time.Now()
	job.StartedAt = &now

	ctx := stream.Context()
	copyReq := &executor.CopyRequest{
		Hosts:    hosts,
		Src:      src,
		Dest:     dest,
		Parallel: parallel,
		Mode:     mode,
		Backup:   req.Backup,
	}

	callback := func(result *executor.CopyResult) {
		job.mu.Lock()
		defer job.mu.Unlock()

		duration := result.EndTime.Sub(result.StartTime)
		job.Results[result.Host] = &HostResult{
			Host:       result.Host,
			Success:    result.Err == nil,
			Error:      errorMsg(result),
			DurationMs: duration.Milliseconds(),
		}

		status := pb.CopyResponse_SUCCESS
		if result.Err != nil {
			status = pb.CopyResponse_FAILED
		}

		stream.Send(&pb.CopyResponse{
			Host:        result.Host,
			Status:      status,
			BytesCopied: result.BytesCopied,
			Error:       errorMsg(result),
			Finished:    true,
		})
	}

	if err := s.executor.Copy(ctx, copyReq, callback); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.mu.Unlock()
		return err
	}

	job.mu.Lock()
	job.Status = StatusCompleted
	completed := time.Now()
	job.CompletedAt = &completed
	job.mu.Unlock()

	return nil
}

// Fetch downloads files from multiple hosts with streaming progress updates.
// It creates a job and streams fetch results as each host completes.
func (s *Server) Fetch(req *pb.FetchRequest, stream pb.Dispatch_FetchServer) error {
	hosts := req.Hosts
	src := req.Src
	dest := req.Dest
	parallel := int(req.Parallel)

	job := &Job{
		ID:        generateJobID(),
		Type:      JobTypeFetch,
		Status:    StatusRunning,
		Hosts:     hosts,
		CreatedAt: time.Now(),
		Results:   make(map[string]*HostResult),
	}

	s.jobMu.Lock()
	s.jobs[job.ID] = job
	s.jobMu.Unlock()

	now := time.Now()
	job.StartedAt = &now

	// 确保目标目录存在
	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("failed to create dest directory: %w", err)
	}

	ctx := stream.Context()
	fetchReq := &executor.FetchRequest{
		Hosts:    hosts,
		Src:      src,
		Dest:     dest,
		Parallel: parallel,
	}

	callback := func(result *executor.FetchResult) {
		job.mu.Lock()
		defer job.mu.Unlock()

		duration := result.EndTime.Sub(result.StartTime)
		job.Results[result.Host] = &HostResult{
			Host:       result.Host,
			Success:    result.Err == nil,
			Error:      errorMsg(result),
			DurationMs: duration.Milliseconds(),
		}

		status := pb.FetchResponse_SUCCESS
		if result.Err != nil {
			status = pb.FetchResponse_FAILED
		}

		stream.Send(&pb.FetchResponse{
			Host:         result.Host,
			Status:       status,
			LocalPath:    result.LocalPath,
			BytesFetched: result.BytesFetched,
			Error:        errorMsg(result),
			Finished:     true,
		})
	}

	if err := s.executor.Fetch(ctx, fetchReq, callback); err != nil {
		job.mu.Lock()
		job.Status = StatusFailed
		job.mu.Unlock()
		return err
	}

	job.mu.Lock()
	job.Status = StatusCompleted
	completed := time.Now()
	job.CompletedAt = &completed
	job.mu.Unlock()

	return nil
}

// Hosts returns information about configured hosts and groups.
// When all=true, returns all hosts across all groups.
// When group is set, returns only hosts in that group.
func (s *Server) Hosts(ctx context.Context, req *pb.HostsRequest) (*pb.HostsResponse, error) {
	var hosts []*pb.HostInfo
	var groups []*pb.GroupInfo

	if req.All {
		// 获取所有主机
		allGroups := s.inv.GetAllGroups()
		for name, addrs := range allGroups {
			groups = append(groups, &pb.GroupInfo{
				Name:  name,
				Hosts: addrs,
				Count: int32(len(addrs)),
			})

			for _, addr := range addrs {
				hosts = append(hosts, &pb.HostInfo{
					Address: addr,
					Group:   name,
					User:    s.inv.GetConfig().SSH.User,
					Port:    int32(s.inv.GetConfig().SSH.Port),
					Online:  true, // TODO: 实际检测
				})
			}
		}
	} else if req.Group != "" {
		// 获取指定组的主机
		allGroups := s.inv.GetAllGroups()
		if addrs, ok := allGroups[req.Group]; ok {
			groups = append(groups, &pb.GroupInfo{
				Name:  req.Group,
				Hosts: addrs,
				Count: int32(len(addrs)),
			})

			for _, addr := range addrs {
				hosts = append(hosts, &pb.HostInfo{
					Address: addr,
					Group:   req.Group,
					User:    s.inv.GetConfig().SSH.User,
					Port:    int32(s.inv.GetConfig().SSH.Port),
					Online:  true,
				})
			}
		}
	}

	return &pb.HostsResponse{
		Hosts:  hosts,
		Groups: groups,
	}, nil
}

// GetJob streams status updates for a specific job until completion.
// The client receives updates every 500ms until the job finishes or times out.
func (s *Server) GetJob(req *pb.JobRequest, stream pb.Dispatch_GetJobServer) error {
	jobID := req.JobId

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.jobMu.RLock()
			job, exists := s.jobs[jobID]
			s.jobMu.RUnlock()

			if !exists {
				return fmt.Errorf("job not found")
			}

			update := s.jobToPbStatus(job)
			if err := stream.Send(update); err != nil {
				return err
			}

			if job.Status == StatusCompleted || job.Status == StatusFailed || job.Status == StatusCancelled {
				return nil
			}

		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-time.After(5 * time.Minute):
			return fmt.Errorf("timeout")
		}
	}
}

// jobToPbStatus 转换 Job 到 pb.JobStatus
func (s *Server) jobToPbStatus(job *Job) *pb.JobStatus {
	job.mu.RLock()
	defer job.mu.RUnlock()

	status := pb.JobStatus_PENDING
	switch job.Status {
	case StatusRunning:
		status = pb.JobStatus_RUNNING
	case StatusCompleted:
		status = pb.JobStatus_COMPLETED
	case StatusFailed:
		status = pb.JobStatus_FAILED
	case StatusCancelled:
		status = pb.JobStatus_CANCELLED
	}

	jobType := pb.JobStatus_EXEC
	switch job.Type {
	case JobTypeCopy:
		jobType = pb.JobStatus_COPY
	case JobTypeFetch:
		jobType = pb.JobStatus_FETCH
	}

	var completedHosts, failedHosts int32
	results := make([]*pb.HostResult, 0, len(job.Results))
	for _, r := range job.Results {
		if r.Success {
			completedHosts++
		} else {
			failedHosts++
		}
		results = append(results, &pb.HostResult{
			Host:       r.Host,
			Success:    r.Success,
			Error:      r.Error,
			DurationMs: r.DurationMs,
		})
	}

	pbStatus := &pb.JobStatus{
		JobId:          job.ID,
		Type:           jobType,
		Status:         status,
		TotalHosts:     int32(len(job.Hosts)),
		CompletedHosts: completedHosts,
		FailedHosts:    failedHosts,
		Results:        results,
	}

	if job.StartedAt != nil {
		pbStatus.StartedAt = job.StartedAt.Unix()
	}
	if job.CompletedAt != nil {
		pbStatus.CompletedAt = job.CompletedAt.Unix()
	}

	return pbStatus
}

// ListJobs returns all jobs matching the specified status filter.
// When status=ALL, returns all jobs regardless of status.
func (s *Server) ListJobs(ctx context.Context, req *pb.ListJobsRequest) (*pb.ListJobsResponse, error) {
	s.jobMu.RLock()
	defer s.jobMu.RUnlock()

	var jobs []*pb.JobStatus

	for _, job := range s.jobs {
		if req.Status != pb.ListJobsRequest_ALL {
			match := false
			switch req.Status {
			case pb.ListJobsRequest_RUNNING:
				match = (job.Status == StatusRunning)
			case pb.ListJobsRequest_COMPLETED:
				match = (job.Status == StatusCompleted)
			case pb.ListJobsRequest_FAILED:
				match = (job.Status == StatusFailed)
			}
			if !match {
				continue
			}
		}

		jobs = append(jobs, s.jobToPbStatus(job))

		if req.Limit > 0 && int32(len(jobs)) >= req.Limit {
			break
		}
	}

	return &pb.ListJobsResponse{
		Jobs:  jobs,
		Total: int32(len(s.jobs)),
	}, nil
}

// CancelJob cancels a running or pending job.
// Only jobs in Running or Pending status can be cancelled.
func (s *Server) CancelJob(ctx context.Context, req *pb.CancelJobRequest) (*pb.CancelJobResponse, error) {
	s.jobMu.Lock()
	defer s.jobMu.Unlock()

	job, exists := s.jobs[req.JobId]
	if !exists {
		return &pb.CancelJobResponse{Success: false, Message: "job not found"}, nil
	}

	if job.Status != StatusRunning && job.Status != StatusPending {
		return &pb.CancelJobResponse{Success: false, Message: "job cannot be cancelled"}, nil
	}

	job.mu.Lock()
	job.Status = StatusCancelled
	job.mu.Unlock()

	return &pb.CancelJobResponse{Success: true, Message: "job cancelled"}, nil
}

// generateJobID generates a unique job identifier based on timestamp.

// errorMsg extracts error message from various result types.

// statusFromError returns a status string based on error state.

// GetInventory returns the inventory (for direct CLI usage).

// GetExecutor returns the executor (for direct CLI usage).
func generateJobID() string {
	return fmt.Sprintf("job-%d", time.Now().UnixNano())
}

// errorMsg extracts error message from various result types.
func errorMsg(r interface{}) string {
	if r == nil {
		return ""
	}
	switch v := r.(type) {
	case error:
		if v != nil {
			return v.Error()
		}
	case *executor.ExecResult:
		if v.Err != nil {
			return v.Err.Error()
		}
	case *executor.CopyResult:
		if v.Err != nil {
			return v.Err.Error()
		}
	case *executor.FetchResult:
		if v.Err != nil {
			return v.Err.Error()
		}
	}
	return ""
}

func statusFromError(err error) string {
	if err == nil {
		return "success"
	}
	return "failed"
}

// GetInventory 返回 inventory（用于 CLI 直接使用）
func (s *Server) GetInventory() *inventory.Inventory {
	return s.inv
}

// GetExecutor 返回 executor（用于 CLI 直接使用）
func (s *Server) GetExecutor() *executor.Executor {
	return s.executor
}
