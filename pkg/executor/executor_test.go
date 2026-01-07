package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	dispatchssh "github.com/liliang-cn/dispatch/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// MockSSHClient implements SSHClient for testing
type MockSSHClient struct {
	t *testing.T
	CmdResponses map[string]struct {
		Stdout   string
		Stderr   string
		ExitCode int
	}
	CmdHistory []string
}

func (m *MockSSHClient) Connect(spec dispatchssh.HostSpec) (*ssh.Client, error) {
	// Use a real TCP listener to avoid net.Pipe deadlocks during handshake
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer l.Close()

	// Handle connection in background
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		m.handleServer(conn)
	}()

	// Connect to local listener
	config := &ssh.ClientConfig{
		User:            "test",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		return nil, err
	}

	c, chans, reqs, err := ssh.NewClientConn(conn, l.Addr().String(), config)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

func (m *MockSSHClient) handleServer(c net.Conn) {
	defer c.Close()
	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}
	
	// Generate a key
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(priv)
	config.AddHostKey(signer)

	_, chans, reqs, err := ssh.NewServerConn(c, config)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}

		go func(in <-chan *ssh.Request) {
			for req := range in {
				switch req.Type {
				case "exec":
					cmd := string(req.Payload[4:])
					m.CmdHistory = append(m.CmdHistory, cmd)
					req.Reply(true, nil)

					var stdout, stderr string
					exitCode := 0
					
					// Handle SCP
					if strings.HasPrefix(cmd, "scp -t") {
						io.Copy(io.Discard, channel)
						exitCode = 0
					} else {
						// Handle other commands
						for k, v := range m.CmdResponses {
							if strings.Contains(cmd, k) {
								stdout = v.Stdout
								stderr = v.Stderr
								exitCode = v.ExitCode
								break
							}
						}
					}

					channel.Write([]byte(stdout))
					channel.Stderr().Write([]byte(stderr))
					channel.SendRequest("exit-status", false, ssh.Marshal(struct{ ExitStatus uint32 }{uint32(exitCode)}))
					channel.Close()
				default:
					req.Reply(true, nil)
				}
			}
		}(requests)
	}
}

// Dummy methods
func (m *MockSSHClient) Exec(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (*dispatchssh.ExecResult, error) { return nil, nil }
func (m *MockSSHClient) ExecStream(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (io.ReadCloser, io.ReadCloser, error) { return nil, nil, nil }
func (m *MockSSHClient) Copy(spec dispatchssh.HostSpec, src, dest string, mode os.FileMode) error { return nil }
func (m *MockSSHClient) Fetch(spec dispatchssh.HostSpec, src, dest string) error { return nil }

func TestUpdate(t *testing.T) {
	// Create temp source file
	tmpFile, err := os.CreateTemp("", "test_src")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	content := "test content"
	tmpFile.WriteString(content)
	tmpFile.Close()

	// Mock inventory
	inv, _ := inventory.New("")
	
	// Create executor
	exec := NewExecutor(inv)
	
	// Setup mock client
	mock := &MockSSHClient{
		t: t,
		CmdResponses: map[string]struct{
			Stdout   string
			Stderr   string
			ExitCode int
		}{
			// Simulate sha256sum check failure (different hash)
			"sha256sum": {Stdout: "differenthash", ExitCode: 0},
		},
	}
	exec.SetBaseClient(mock)

	// Test Update with Backup
	req := &UpdateRequest{
		Hosts:  []string{"localhost"},
		Src:    tmpFile.Name(),
		Dest:   "/tmp/dest",
		Backup: true,
	}

	ctx := context.Background()
	err = exec.Update(ctx, req, func(res *UpdateResult) {
		if res.Err != nil {
			t.Errorf("Update failed: %v", res.Err)
		}
		if res.Skipped {
			t.Error("Update should not be skipped")
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify commands
	backupCalled := false
	for _, cmd := range mock.CmdHistory {
		if strings.Contains(cmd, "cp /tmp/dest /tmp/dest.bak") {
			backupCalled = true
		}
	}
		if !backupCalled {
			t.Error("Backup command was not called")
		}
	}
	
	func TestExec_Success(t *testing.T) {
		// Mock inventory
		inv, _ := inventory.New("")
		exec := NewExecutor(inv)
		
		mock := &MockSSHClient{
			t: t,
			CmdResponses: map[string]struct{
				Stdout   string
				Stderr   string
				ExitCode int
			}{
				"echo hello": {Stdout: "hello\n", ExitCode: 0},
			},
		}
		exec.SetBaseClient(mock)
	
		req := &ExecRequest{
			Hosts: []string{"localhost"},
			Cmd:   "echo hello",
		}
	
		ctx := context.Background()
		var output string
		err := exec.Exec(ctx, req, func(res *ExecResult) {
			output = string(res.Output)
			if res.ExitCode != 0 {
				t.Errorf("Expected exit code 0, got %d", res.ExitCode)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
	
		if output != "hello\n" {
			t.Errorf("Expected output 'hello\\n', got '%s'", output)
		}
	}
	
	func TestExec_ExitError(t *testing.T) {
		inv, _ := inventory.New("")
		exec := NewExecutor(inv)
		
		mock := &MockSSHClient{
			t: t,
			CmdResponses: map[string]struct{
				Stdout   string
				Stderr   string
				ExitCode int
			}{
				"fail_cmd": {Stdout: "", Stderr: "failed", ExitCode: 1},
			},
		}
		exec.SetBaseClient(mock)
	
		req := &ExecRequest{
			Hosts: []string{"localhost"},
			Cmd:   "fail_cmd",
		}
	
		var exitCode int
		exec.Exec(context.Background(), req, func(res *ExecResult) {
			exitCode = res.ExitCode
		})
	
			if exitCode != 1 {
				t.Errorf("Expected exit code 1, got %d", exitCode)
			}
		}
		
		func TestCopy(t *testing.T) {
			tmpFile, err := os.CreateTemp("", "src")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpFile.Name())
			tmpFile.WriteString("content")
			tmpFile.Close()
		
			inv, _ := inventory.New("")
			exec := NewExecutor(inv)
			mock := &MockSSHClient{t: t}
			exec.SetBaseClient(mock)
		
			req := &CopyRequest{
				Hosts: []string{"localhost"},
				Src:   tmpFile.Name(),
				Dest:  "/remote/dest",
			}
		
			err = exec.Copy(context.Background(), req, func(res *CopyResult) {
				if res.Err != nil {
					t.Errorf("Copy failed: %v", res.Err)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		
		func TestFetch(t *testing.T) {
			// Need a dest dir
			tmpDir, err := os.MkdirTemp("", "dest")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)
		
			inv, _ := inventory.New("")
			exec := NewExecutor(inv)
			mock := &MockSSHClient{t: t}
			exec.SetBaseClient(mock)
		
				req := &FetchRequest{
					Hosts: []string{"localhost"},
					Src:   "/remote/src",
					Dest:  filepath.Join(tmpDir, "file"),
				}
						err = exec.Fetch(context.Background(), req, func(res *FetchResult) {
				// Mock Fetch does nothing, so file won't exist locally unless mocked
				// But result.Err should be nil if mock returns nil
				if res.Err != nil {
					t.Errorf("Fetch failed: %v", res.Err)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		