package server

import (
	"context"
	"os"
	"testing"

	pb "github.com/liliang-cn/dispatch/proto"
)

func TestServer_Hosts(t *testing.T) {
	// Create temporary config file
	content := `
[hosts.web]
addresses = ["192.168.1.10"]
[hosts.db]
addresses = ["192.168.1.20"]
`
	tmpfile, err := os.CreateTemp("", "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	tmpfile.Write([]byte(content))
	tmpfile.Close()

	srv, err := NewServer(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test List All
	ctx := context.Background()
	resp, err := srv.Hosts(ctx, &pb.HostsRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(resp.Groups) != 2 {
		t.Errorf("Expected 2 groups, got %d", len(resp.Groups))
	}

	// Test Specific Group
	resp, err = srv.Hosts(ctx, &pb.HostsRequest{Group: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Hosts) != 1 || resp.Hosts[0].Address != "192.168.1.10" {
		t.Errorf("Unexpected hosts in web group: %v", resp.Hosts)
	}
}

func TestServer_JobManagement(t *testing.T) {
	srv, _ := NewServer("")
	
	// Create a dummy job
	jobID := "test-job"
	srv.jobMu.Lock()
	srv.jobs[jobID] = &Job{
		ID:     jobID,
		Status: StatusRunning,
		Hosts:  []string{"host1"},
	}
	srv.jobMu.Unlock()

	// Test Cancel
	ctx := context.Background()
	resp, err := srv.CancelJob(ctx, &pb.CancelJobRequest{JobId: jobID})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Errorf("Cancel failed: %s", resp.Message)
	}

	srv.jobMu.RLock()
	if srv.jobs[jobID].Status != StatusCancelled {
		t.Error("Job status was not updated to cancelled")
	}
	srv.jobMu.RUnlock()
}
