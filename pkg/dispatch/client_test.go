package dispatch

import (
	"testing"
	"time"
)

func TestNew_NilConfig(t *testing.T) {
	// Test that New(nil) works without any configuration
	// It should read from ~/.ssh/config if available
	client, err := New(nil)
	if err != nil {
		t.Fatalf("Failed to create client with nil config: %v", err)
	}
	if client == nil {
		t.Fatal("Expected non-nil client")
	}

	// Verify defaults are applied
	invCfg := client.inv.GetConfig()
	if invCfg.SSH.Port != 22 {
		t.Errorf("Expected default port 22, got %d", invCfg.SSH.Port)
	}
	if invCfg.Exec.Parallel != 10 {
		t.Errorf("Expected default parallel 10, got %d", invCfg.Exec.Parallel)
	}
}

func TestNew_Config(t *testing.T) {
	cfg := &Config{
		SSH: &SSHConfig{
			User: "test_user",
			Port: 2222,
		},
		Exec: &ExecConfig{
			Parallel: 50,
			Timeout:  60,
		},
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Verify config was applied to inventory
	invCfg := client.inv.GetConfig()
	
	if invCfg.SSH.User != "test_user" {
		t.Errorf("Expected SSH user test_user, got %s", invCfg.SSH.User)
	}
	if invCfg.SSH.Port != 2222 {
		t.Errorf("Expected SSH port 2222, got %d", invCfg.SSH.Port)
	}
	if invCfg.Exec.Parallel != 50 {
		t.Errorf("Expected parallel 50, got %d", invCfg.Exec.Parallel)
	}
}

func TestWithOptions(t *testing.T) {
	// compile check for option functions
	_ = WithParallel(10)
	_ = WithTimeout(10 * time.Second)
	_ = WithEnv(map[string]string{"A": "B"})
	_ = WithDir("/tmp")
	_ = WithInput("input")
	_ = WithStreamCallback(func(host, typ string, data []byte) {})
	_ = WithCopyMode(0755)
	_ = WithBackup(true)
	_ = WithUpdateBackup(true)
}
