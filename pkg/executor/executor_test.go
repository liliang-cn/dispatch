package executor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liliang-cn/dispatch/pkg/inventory"
	dispatchssh "github.com/liliang-cn/dispatch/pkg/ssh"
	"golang.org/x/crypto/ssh"
)

// MockSSHClient implements SSHClient for testing
type MockSSHClient struct {
	t            *testing.T
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
func (m *MockSSHClient) Exec(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (*dispatchssh.ExecResult, error) {
	return nil, nil
}
func (m *MockSSHClient) ExecStream(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, nil
}
func (m *MockSSHClient) Copy(spec dispatchssh.HostSpec, src, dest string, mode os.FileMode) error {
	return nil
}
func (m *MockSSHClient) Fetch(spec dispatchssh.HostSpec, src, dest string) error {
	return nil
}

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

	// Setup mock client - use non-local host to avoid local execution
	mock := &MockSSHClient{
		t: t,
		CmdResponses: map[string]struct {
			Stdout   string
			Stderr   string
			ExitCode int
		}{
			// Simulate sha256sum check failure (different hash)
			"sha256sum": {Stdout: "differenthash", ExitCode: 0},
		},
	}
	exec.SetBaseClient(mock)

	// Test Update with Backup - use remote host, not localhost
	req := &UpdateRequest{
		Hosts:  []string{"testhost"},
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

	// Verify backup command was called
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
		CmdResponses: map[string]struct {
			Stdout   string
			Stderr   string
			ExitCode int
		}{
			"echo hello": {Stdout: "hello\n", ExitCode: 0},
		},
	}
	exec.SetBaseClient(mock)

	// Use non-local host
	req := &ExecRequest{
		Hosts: []string{"testhost"},
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
		CmdResponses: map[string]struct {
			Stdout   string
			Stderr   string
			ExitCode int
		}{
			"fail_cmd": {Stdout: "", Stderr: "failed", ExitCode: 1},
		},
	}
	exec.SetBaseClient(mock)

	// Use non-local host
	req := &ExecRequest{
		Hosts: []string{"testhost"},
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

	// Use non-local host
	req := &CopyRequest{
		Hosts: []string{"testhost"},
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
	// Create a temp file to simulate fetched content
	tmpFile, err := os.CreateTemp("", "fetched")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.WriteString("fetched content")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	inv, _ := inventory.New("")
	exec := NewExecutor(inv)

	// Create mock that writes to dest
	mock := &MockSSHClient{
		t: t,
		CmdResponses: map[string]struct {
			Stdout   string
			Stderr   string
			ExitCode int
		}{
			"stat": {Stdout: "15", ExitCode: 0}, // Return file size
		},
	}
	exec.SetBaseClient(mock)

	// Use non-local host
	req := &FetchRequest{
		Hosts: []string{"testhost"},
		Src:   "/remote/src",
		Dest:  tmpFile.Name(),
	}

	err = exec.Fetch(context.Background(), req, func(res *FetchResult) {
		// Mock doesn't actually create file, but should not return error
		// since the mock Copy returns nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentOperationLifecycle guards against the shared-executor race:
// one operation's endOperation must not tear down the connection cache while
// other concurrent operations are still using it (previously panicked with
// "assignment to entry in nil map" in getConnection).
func TestConcurrentOperationLifecycle(t *testing.T) {
	inv, _ := inventory.New("")
	e := NewExecutor(inv)
	e.SetBaseClient(&nilSSHClient{})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.beginOperation(); err != nil {
				t.Errorf("beginOperation: %v", err)
				return
			}
			// Simulate the cache write that crashed on a nil map.
			e.connMu.Lock()
			if e.connCache == nil {
				t.Error("connCache is nil during an active operation")
			}
			e.connMu.Unlock()
			e.endOperation()
		}()
	}
	wg.Wait()

	e.connMu.Lock()
	defer e.connMu.Unlock()
	if e.opCount != 0 {
		t.Errorf("opCount = %d after all operations finished, want 0", e.opCount)
	}
	if e.connCache != nil {
		t.Error("connCache should be released after the last operation")
	}
}

// nilSSHClient satisfies SSHClient without making network connections.
type nilSSHClient struct{}

func (nilSSHClient) Connect(spec dispatchssh.HostSpec) (*ssh.Client, error) {
	return nil, fmt.Errorf("not implemented")
}
func (nilSSHClient) Exec(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (*dispatchssh.ExecResult, error) {
	return nil, fmt.Errorf("not implemented")
}
func (nilSSHClient) ExecStream(spec dispatchssh.HostSpec, cmd string, input string, timeout time.Duration) (io.ReadCloser, io.ReadCloser, error) {
	return nil, nil, fmt.Errorf("not implemented")
}
func (nilSSHClient) Copy(spec dispatchssh.HostSpec, src, dest string, mode os.FileMode) error {
	return fmt.Errorf("not implemented")
}
func (nilSSHClient) Fetch(spec dispatchssh.HostSpec, src, dest string) error {
	return fmt.Errorf("not implemented")
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{`plain`, `'plain'`},
		{`has space`, `'has space'`},
		{`a'b`, `'a'\''b'`},
		{`$HOME and $(date)`, `'$HOME and $(date)'`},
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBuildCommandPreservesVariables guards the quoting contract: commands
// defining and using their own shell variables must arrive at the target
// shell verbatim. The previous double-quote wrapping let the OUTER shell
// (the SSH login shell, or the extra local sh) expand $f to an empty string
// before the command ran.
func TestBuildCommandPreservesVariables(t *testing.T) {
	inv, _ := inventory.New("")
	e := NewExecutor(inv)

	cmd := `f=/tmp/x; echo "$f"`
	built := e.buildCommand(cmd, nil, "")

	// Remote form: shell -c '<verbatim payload>' (shell comes from config)
	if !strings.HasSuffix(built, ` -c 'f=/tmp/x; echo "$f"'`) {
		t.Errorf("buildCommand = %q, want single-quoted verbatim payload", built)
	}

	// Round-trip through a real shell the way the remote side parses it:
	// the output must be the variable's value, not an empty line.
	out, err := exec.Command("/bin/sh", "-c", built).Output()
	if err != nil {
		t.Fatalf("shell round-trip failed: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "/tmp/x" {
		t.Errorf("round-trip output = %q, want %q (outer shell expanded $f)", got, "/tmp/x")
	}
}

func TestBuildShellLineEnvAndDir(t *testing.T) {
	inv, _ := inventory.New("")
	e := NewExecutor(inv)

	line := e.buildShellLine("echo hi", map[string]string{"B": "2", "A": "it's"}, "/tmp")
	want := `cd '/tmp' && A='it'\''s' B='2' echo hi`
	if line != want {
		t.Errorf("buildShellLine = %q, want %q", line, want)
	}
}
